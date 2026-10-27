package pgproxy

import (
	"iter"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/recordstore/recordresults"
	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/tracing"
	tracesql "github.com/flanksource/telltale/pkg/tracing/sql"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

type UnifiedParams struct {
	Params
	Connections []string `json:"connections,omitempty" clicky:"title=Connections,desc=Connections to observe"`
	MinDuration string   `json:"minDuration,omitempty" clicky:"title=Minimum duration,desc=Record statements that ran at least this long"`
}

func (p UnifiedParams) sqlParams() tracesql.Params {
	return tracesql.Params{Connections: p.Connections, MinDuration: p.MinDuration, Target: p.Target}
}
func (p UnifiedParams) Validate() error {
	if p.Target.Seam == target.Proxy {
		return p.Params.Validate()
	}
	return p.sqlParams().Validate()
}

type unified struct {
	self interface {
		tracing.TraceHandler[tracesql.Params, tracesql.StatementRow]
		tracing.Preparer[tracesql.Params, tracesql.StatementRow]
		AgentAvailable(string, target.Target) (bool, error)
	}
}

func (u unified) AgentAvailable(probe string, t target.Target) (bool, error) {
	return u.self.AgentAvailable(probe, t)
}
func (unified) Params() UnifiedParams {
	return UnifiedParams{Params: pgProxy{}.Params(), Connections: []string{"self"}}
}
func (unified) Schema() recordresults.ResultType[tracesql.StatementRow] { return tracesql.ResultType() }
func (u unified) Prepare(ctx dbcontext.Context, p UnifiedParams, out tracing.Emitter[tracesql.StatementRow]) (dbcontext.Context, func() error, error) {
	if p.Target.Seam == target.Proxy {
		return (pgProxy{}).Prepare(ctx, p.Params, out)
	}
	return u.self.Prepare(ctx, p.sqlParams(), out)
}
func (u unified) Handle(ctx dbcontext.Context, p UnifiedParams, out tracing.Emitter[tracesql.StatementRow], rows tracing.Records[tracesql.StatementRow]) error {
	if p.Target.Seam == target.Proxy {
		return (pgProxy{}).Handle(ctx, p.Params, out, rows)
	}
	return u.self.Handle(ctx, p.sqlParams(), out, rows)
}
func (unified) Summarize(rows iter.Seq[tracesql.StatementRow]) (map[string]any, error) {
	return tracesql.WindowVars(rows), nil
}

func UnifiedKind(registry *agentclient.Registry) *tracing.Handler[UnifiedParams, tracesql.StatementRow] {
	return tracing.NewHandler[UnifiedParams, tracesql.StatementRow](unified{self: tracesql.NewCapture(registry)}, tracing.Capabilities{Live: true}, target.Self, target.Agent, target.Proxy).
		WithSecretMasking().WithTruncation(maxValueBytes)
}
