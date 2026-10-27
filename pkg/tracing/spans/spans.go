package spans

import (
	"encoding/json"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/recordstore/recordresults"
	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/seam"
	"github.com/flanksource/telltale/pkg/tracing/target"
	telltale "github.com/flanksource/telltale/sdk"
)

const Kind = "spans"

type Params struct {
	Target target.Target `json:"target,omitzero"`
}

type capture struct {
	agent seam.AgentSeam[Params, telltale.Span]
}

func (c capture) AgentAvailable(probe string, t target.Target) (bool, error) {
	return c.agent.Available(probe, t)
}
func (capture) Params() Params { return Params{} }
func (capture) Schema() recordresults.ResultType[telltale.Span] {
	return recordresults.ResultType[telltale.Span]{Title: "Spans", TimeColumn: "startedAt", Follow: true}
}
func (c capture) Prepare(ctx dbcontext.Context, p Params, out tracing.Emitter[telltale.Span]) (dbcontext.Context, func() error, error) {
	return c.agent.Prepare(ctx, p, out)
}
func (c capture) Handle(ctx dbcontext.Context, p Params, out tracing.Emitter[telltale.Span], _ tracing.Records[telltale.Span]) error {
	return c.agent.Handle(ctx, p, out)
}

func NewKind(registry *agentclient.Registry) *tracing.Handler[Params, telltale.Span] {
	agent := seam.AgentSeam[Params, telltale.Span]{Probe: Kind, Registry: registry, Target: func(p Params) target.Target { return p.Target }, ArmParams: func(p Params) (json.RawMessage, error) { return json.Marshal(p) }}
	return tracing.NewHandler[Params, telltale.Span](capture{agent}, tracing.Capabilities{Live: true}, target.Self, target.Agent).WithSecretMasking()
}
