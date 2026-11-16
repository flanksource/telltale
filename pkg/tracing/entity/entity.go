// Package entity records the clicky entity operations an agent runs, armed
// on the entity probe github.com/flanksource/clicky/telltale registers.
package entity

import (
	"encoding/json"
	"fmt"
	"iter"
	"path/filepath"
	"time"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/recordstore/recordresults"
	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/seam"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

const Kind = "entity"

// Options choose the operations a capture records.
type Options struct {
	Entity     string `json:"entity,omitempty" clicky:"desc=Entity name glob"`
	Verb       string `json:"verb,omitempty" clicky:"desc=Verb glob"`
	ErrorsOnly bool   `json:"errorsOnly,omitempty" clicky:"desc=Record failed operations only"`
}

// Params are a capture's Options in the agent its target names.
type Params struct {
	Target target.Target `json:"target,omitzero"`
	Options
}

// Validate is the static check of the entity and verb globs.
func (p Options) Validate() error {
	for _, glob := range []string{p.Entity, p.Verb} {
		if _, err := filepath.Match(glob, ""); err != nil {
			return fmt.Errorf("glob %q: %w", glob, err)
		}
	}
	return nil
}

// Operation mirrors clicky/telltale's EntityRecord: one completed operation,
// with masked parameters and never its result.
type Operation struct {
	tracing.Truncation
	Entity     string            `json:"entity"`
	Verb       string            `json:"verb"`
	Surface    string            `json:"surface,omitempty"`
	TargetID   string            `json:"targetId,omitempty"`
	Admin      bool              `json:"admin,omitempty"`
	ReadOnly   *bool             `json:"readOnly,omitempty"`
	Parameters map[string]string `json:"parameters,omitempty"`
	Args       []string          `json:"args,omitempty"`
	StartedAt  time.Time         `json:"startedAt"`
	DurationMs float64           `json:"durationMs"`
	Error      string            `json:"error,omitempty"`
	Resource   map[string]string `json:"resource,omitempty"`
}

type capture struct {
	agent seam.AgentSeam[Params, Operation]
}

func (c capture) AgentAvailable(probe string, t target.Target) (bool, error) {
	return c.agent.Available(probe, t)
}
func (capture) Params() Params { return Params{} }
func (capture) Schema() recordresults.ResultType[Operation] {
	return recordresults.ResultType[Operation]{Title: "Entity operations", TimeColumn: "startedAt", Follow: true}
}
func (c capture) Prepare(ctx dbcontext.Context, p Params, out tracing.Emitter[Operation]) (dbcontext.Context, func() error, error) {
	return c.agent.Prepare(ctx, p, out)
}
func (c capture) Handle(ctx dbcontext.Context, p Params, out tracing.Emitter[Operation], _ tracing.Records[Operation]) error {
	return c.agent.Handle(ctx, p, out)
}

func (capture) Summarize(rows iter.Seq[Operation]) (map[string]any, error) {
	count, failed, duration := 0, 0, 0.0
	entities := map[string]int{}
	for row := range rows {
		count++
		duration += row.DurationMs
		entities[row.Entity]++
		if row.Error != "" {
			failed++
		}
	}
	return map[string]any{"operations": count, "failures": failed, "durationMs": duration, "entities": entities}, nil
}

// NewKind is the entity kind, armed only through an agent: telltale itself
// registers no entity probe.
func NewKind(registry *agentclient.Registry) *tracing.Handler[Params, Operation] {
	agent := seam.AgentSeam[Params, Operation]{Probe: Kind, Registry: registry, Target: func(p Params) target.Target { return p.Target }, ArmParams: func(p Params) (json.RawMessage, error) { return json.Marshal(p) }}
	return tracing.NewHandler[Params, Operation](capture{agent}, tracing.Capabilities{Live: true}, target.Agent).WithSecretMasking()
}
