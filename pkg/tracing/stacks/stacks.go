// Package stacks is a trace kind that dumps the stacks of the processes of a
// tree at each checkpoint its host marks, storing one record per goroutine of
// a Go dump, or one per dump of a native one. Dumps go over the transports a
// host registers in Dumpers.
package stacks

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/google/uuid"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/commons-db/recordstore/recordresults"
	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/seam"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

// Kind is the name a host registers the kind as.
const Kind = "stacks"

// maxRawBytes cuts a stored dump longer than this.
const maxRawBytes = 1 << 20

// Params choose the tree whose processes a capture dumps.
type Params struct {
	Target target.Target `json:"target,omitzero"`
}

func (p Params) Validate() error {
	if p.Target.PID < 0 {
		return fmt.Errorf("pid %d is not a process id", p.Target.PID)
	}
	return nil
}

func (p Params) root() int {
	if p.Target.PID == 0 {
		return os.Getpid()
	}
	return p.Target.PID
}

// Frame is one call of a stack.
type Frame struct {
	Func string `json:"func"`
	File string `json:"file,omitempty"`
	Line int    `json:"line,omitempty"`
}

// Stack is the record the kind stores: one goroutine of a Go dump, or the
// whole of a native one, or why a dump failed.
type Stack struct {
	tracing.Truncation
	CheckpointID string    `json:"checkpointId" pretty:"hide" filter:"exact"`
	TakenAt      time.Time `json:"takenAt" pretty:"label=Taken" sort:"takenAt"`
	Reason       string    `json:"reason,omitempty" filter:"terms"`
	Transport    string    `json:"transport" filter:"terms"`
	Key          string    `json:"key" pretty:"hide" filter:"exact"`
	PID          int       `json:"pid" pretty:"label=PID" filter:"terms"`
	Format       string    `json:"format" filter:"terms"`
	GoroutineID  int       `json:"goroutineId,omitempty" pretty:"label=Goroutine"`
	State        string    `json:"state,omitempty" filter:"terms"`
	// WaitMinutes is how long the goroutine has waited in its state, as the
	// dump rounds it.
	WaitMinutes    int     `json:"waitMinutes,omitempty" pretty:"label=Waiting (min)" sort:"waitMinutes"`
	LockedToThread bool    `json:"lockedToThread,omitempty"`
	Frames         []Frame `json:"frames,omitempty" pretty:"hide"`
	CreatedBy      *Frame  `json:"createdBy,omitempty" pretty:"hide"`
	Raw            string  `json:"raw" pretty:"hide" filter:"none"`
	Error          string  `json:"error,omitempty"`
}

// CheckpointParams are what a checkpoint dumps: the stacks of the process
// Key or PID names, the root when neither does, over Transport, the first
// available non-destructive one when empty, or Dump, a dump taken elsewhere,
// such as the goroutines a Go test printed as SIGQUIT killed it, which takes
// no transport. Reason, such as "periodic", "timeout" or "manual", is
// recorded with the stacks.
type CheckpointParams struct {
	Key       string `json:"key,omitempty"`
	PID       int    `json:"pid,omitempty"`
	Transport string `json:"transport,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Dump      *Dump  `json:"dump,omitempty"`
}

func (p CheckpointParams) Validate() error {
	switch {
	case p.Key != "" && p.PID != 0:
		return fmt.Errorf("name a process by key %q or pid %d, not both", p.Key, p.PID)
	case p.PID < 0:
		return fmt.Errorf("pid %d is not a process id", p.PID)
	case p.Dump == nil:
		return nil
	case p.Transport != "":
		return fmt.Errorf("a dump taken elsewhere takes no transport, given %q", p.Transport)
	case p.Key == "" && p.PID == 0:
		return errors.New("a dump taken elsewhere needs the key or pid of the process it dumped")
	}
	return p.Dump.Validate()
}

type stacks struct {
	dumpers *Dumpers
	agent   seam.AgentSeam[Params, Stack]
}

func (s stacks) AgentAvailable(probe string, selection target.Target) (bool, error) {
	return s.agent.Available(probe, selection)
}

func (stacks) Params() Params { return Params{} }

func (stacks) Schema() recordresults.ResultType[Stack] {
	return recordresults.ResultType[Stack]{
		Title: "Stack dumps", TimeColumn: "takenAt", Follow: true,
		Indexes:    []recordstore.IndexDef{{Columns: []string{"key", "takenAt"}}},
		Compressed: []string{"raw"}, SearchColumns: []string{"state", "frames"},
	}
}

// Prepare refuses a root that is not running, before the session starts.
func (s stacks) Prepare(ctx dbcontext.Context, params Params, out tracing.Emitter[Stack]) (dbcontext.Context, func() error, error) {
	if params.Target.Seam == target.Agent || params.Target.Seam == target.Self {
		return s.agent.Prepare(ctx, params, out)
	}
	if _, err := Resolve(ctx, params.root(), "", 0); err != nil {
		return ctx, nil, err
	}
	return ctx, nil, nil
}

// Handle waits for the capture to stop: the kind reads only at checkpoints.
func (s stacks) Handle(ctx dbcontext.Context, params Params, out tracing.Emitter[Stack], _ tracing.Records[Stack]) error {
	if params.Target.Seam == target.Agent || params.Target.Seam == target.Self {
		return s.agent.Handle(ctx, params, out)
	}
	<-ctx.Done()
	return nil
}

// NewKind is the stacks kind dumping over dumpers, its dumps cut at 1MiB.
func NewKind(registry *agentclient.Registry, dumpers *Dumpers) *tracing.Handler[Params, Stack] {
	if dumpers == nil {
		panic("stacks: NewKind needs the host's Dumpers")
	}
	agent := seam.AgentSeam[Params, Stack]{Probe: Kind, Registry: registry, Target: func(p Params) target.Target { return p.Target }, DecodeBlob: func(blob agentclient.Blob, resource map[string]string) ([]Stack, error) {
		if blob.Info.Name != "stacks" || blob.Info.ContentType != "text/plain" {
			return nil, fmt.Errorf("unexpected stacks blob %s (%s)", blob.Info.Name, blob.Info.ContentType)
		}
		pid, err := strconv.Atoi(resource["process.pid"])
		if err != nil {
			return nil, fmt.Errorf("agent process pid: %w", err)
		}
		var request CheckpointParams
		if len(blob.CheckpointParams) > 0 {
			if err := json.Unmarshal(blob.CheckpointParams, &request); err != nil {
				return nil, fmt.Errorf("stacks checkpoint params: %w", err)
			}
		}
		return Records(Stack{CheckpointID: newCheckpointID(), Reason: request.Reason, PID: pid, Transport: "agent"}, Dump{Format: FormatGo, Text: string(blob.Data), CollectedAt: time.Now().UTC()}), nil
	}}
	return tracing.NewHandler[Params, Stack](stacks{dumpers: dumpers, agent: agent}, tracing.Capabilities{Live: true}, target.Process, target.Agent, target.Self).
		WithTruncation(maxRawBytes)
}

func newCheckpointID() string { return uuid.NewString() }
