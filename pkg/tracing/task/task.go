// Package task records the clicky tasks an agent runs, armed on the task
// probe github.com/flanksource/clicky/telltale registers.
package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"path/filepath"

	clickytask "github.com/flanksource/clicky/task"
	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/recordstore/recordresults"
	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/seam"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

const Kind = "task"

// Options choose the tasks a capture records and how often it keeps progress.
type Options struct {
	Name               string `json:"name,omitempty" clicky:"desc=Task name glob"`
	Group              string `json:"group,omitempty" clicky:"desc=Parent group name glob"`
	ProgressIntervalMs int    `json:"progressIntervalMs,omitempty" clicky:"desc=Minimum milliseconds between progress records of a task; 0 takes the agent's default"`
}

// Params are a capture's Options in the agent its target names.
type Params struct {
	Target target.Target `json:"target,omitzero"`
	Options
}

// Validate is the static check of the globs and the progress interval.
func (p Options) Validate() error {
	for _, glob := range []string{p.Name, p.Group} {
		if _, err := filepath.Match(glob, ""); err != nil {
			return fmt.Errorf("glob %q: %w", glob, err)
		}
	}
	if p.ProgressIntervalMs < 0 {
		return errors.New("progressIntervalMs must not be negative")
	}
	return nil
}

// Snapshot mirrors clicky/telltale's TaskRecord: a task at a status
// transition, a throttled progress update, or (phase baseline) a task already
// present when the capture armed. It carries no logs or output.
type Snapshot struct {
	tracing.Truncation
	Phase string `json:"phase"`
	clickytask.TaskSnapshot
	Resource map[string]string `json:"resource,omitempty"`
}

type capture struct {
	agent seam.AgentSeam[Params, Snapshot]
}

func (c capture) AgentAvailable(probe string, t target.Target) (bool, error) {
	return c.agent.Available(probe, t)
}
func (capture) Params() Params { return Params{} }
func (capture) Schema() recordresults.ResultType[Snapshot] {
	return recordresults.ResultType[Snapshot]{Title: "Tasks", Follow: true}
}
func (c capture) Prepare(ctx dbcontext.Context, p Params, out tracing.Emitter[Snapshot]) (dbcontext.Context, func() error, error) {
	return c.agent.Prepare(ctx, p, out)
}
func (c capture) Handle(ctx dbcontext.Context, p Params, out tracing.Emitter[Snapshot], _ tracing.Records[Snapshot]) error {
	return c.agent.Handle(ctx, p, out)
}

// Summarize counts the tasks seen and each one's last status.
func (capture) Summarize(rows iter.Seq[Snapshot]) (map[string]any, error) {
	last := map[string]string{}
	for row := range rows {
		last[row.ID] = row.Status
	}
	statuses := map[string]int{}
	for _, status := range last {
		statuses[status]++
	}
	return map[string]any{"tasks": len(last), "statuses": statuses}, nil
}

// NewKind is the task kind, armed only through an agent: telltale itself
// registers no task probe.
func NewKind(registry *agentclient.Registry) *tracing.Handler[Params, Snapshot] {
	agent := seam.AgentSeam[Params, Snapshot]{Probe: Kind, Registry: registry, Target: func(p Params) target.Target { return p.Target }, ArmParams: func(p Params) (json.RawMessage, error) { return json.Marshal(p) }}
	return tracing.NewHandler[Params, Snapshot](capture{agent}, tracing.Capabilities{Live: true}, target.Agent).WithSecretMasking()
}
