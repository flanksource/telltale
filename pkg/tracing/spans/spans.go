package spans

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/commons-db/recordstore/recordresults"
	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/opensearch"
	"github.com/flanksource/telltale/pkg/tracing/seam"
	"github.com/flanksource/telltale/pkg/tracing/target"
	telltale "github.com/flanksource/telltale/sdk"
)

const Kind = "spans"

// Params select where spans come from: the SDK in telltale itself or an
// agent, or, on the host seam, the span documents of an OpenSearch index.
type Params struct {
	Target target.Target `json:"target,omitzero"`
	Options
}

// Options choose the index spans are read from on the host seam; the SDK and
// agent seams take none.
type Options struct {
	OpenSearch *opensearch.Params `json:"opensearch,omitempty" clicky:"title=OpenSearch,desc=Index to read spans from on the host seam"`
}

// Validate is the static check of the index options, whichever seam reads
// them.
func (p Options) Validate() error {
	if p.OpenSearch == nil {
		return nil
	}
	return p.OpenSearch.Validate()
}

func (p Params) Validate() error {
	if p.Target.Seam != target.Host {
		if p.OpenSearch != nil {
			return errors.New("opensearch params read spans on the host seam: add --seam host")
		}
		return nil
	}
	if p.OpenSearch == nil {
		return errors.New("the host seam reads spans from OpenSearch: opensearch.connection is required")
	}
	return p.Options.Validate()
}

// ResultType is how every store keeps spans: keyed by trace and span ID,
// timed by their start, with the receiver's native OTLP compressed.
func ResultType() recordresults.ResultType[telltale.Span] {
	return recordresults.ResultType[telltale.Span]{
		Kind: Kind, Title: "Spans", TimeColumn: "startedAt", KeyColumn: "key", Follow: true,
		Compressed: []string{"native"},
		Indexes:    []recordstore.IndexDef{{Columns: []string{"traceId"}}, {Columns: []string{"service", "startedAt"}}},
	}
}

// Decode reads a stored spans row back, keeping numbers exact.
func Decode(row query.Row) (telltale.Span, error) {
	encoded, err := json.Marshal(row)
	if err != nil {
		return telltale.Span{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var span telltale.Span
	if err := decoder.Decode(&span); err != nil {
		return telltale.Span{}, fmt.Errorf("decode span row: %w", err)
	}
	return span, nil
}

type capture struct {
	agent seam.AgentSeam[Params, telltale.Span]
}

func (c capture) AgentAvailable(probe string, t target.Target) (bool, error) {
	return c.agent.Available(probe, t)
}
func (capture) Params() Params                                  { return Params{} }
func (capture) Schema() recordresults.ResultType[telltale.Span] { return ResultType() }
func (c capture) Prepare(ctx dbcontext.Context, p Params, out tracing.Emitter[telltale.Span]) (dbcontext.Context, func() error, error) {
	if p.Target.Seam == target.Host {
		return ctx, nil, nil
	}
	return c.agent.Prepare(ctx, p, out)
}
func (c capture) Handle(ctx dbcontext.Context, p Params, out tracing.Emitter[telltale.Span], _ tracing.Records[telltale.Span]) error {
	if p.Target.Seam == target.Host {
		return opensearch.Handle(ctx, *p.OpenSearch, out)
	}
	return c.agent.Handle(ctx, p, normalizing{out})
}

// normalizing normalizes agent spans once the agent's resource is merged into
// them, so their service comes from it.
type normalizing struct{ tracing.Emitter[telltale.Span] }

func (n normalizing) Emit(ctx context.Context, span telltale.Span) error {
	if err := span.Normalize(); err != nil {
		return err
	}
	return n.Emitter.Emit(ctx, span)
}

func (n normalizing) TryEmit(span telltale.Span) bool {
	if err := span.Normalize(); err != nil {
		n.Warn(err.Error())
		return false
	}
	return n.Emitter.TryEmit(span)
}

func NewKind(registry *agentclient.Registry) *tracing.Handler[Params, telltale.Span] {
	agent := seam.AgentSeam[Params, telltale.Span]{Probe: Kind, Registry: registry, Target: func(p Params) target.Target { return p.Target }, ArmParams: func(p Params) (json.RawMessage, error) { return json.Marshal(p) }}
	return tracing.NewHandler[Params, telltale.Span](capture{agent}, tracing.Capabilities{Live: true, Historical: true, Follow: true}, target.Self, target.Agent, target.Host).
		WithSecretMasking("key")
}
