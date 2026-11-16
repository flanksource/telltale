package httptop

import (
	"encoding/json"
	"fmt"
	"time"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/commons-db/recordstore/recordresults"
	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/tracing"
	tracehttp "github.com/flanksource/telltale/pkg/tracing/http"
	"github.com/flanksource/telltale/pkg/tracing/seam"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

const Kind = "http_top"

type Options struct {
	tracehttp.Selection
	Poll       string `json:"poll,omitempty" clicky:"title=Poll,desc=Sampling interval such as 5s"`
	Top        int    `json:"top,omitempty" clicky:"title=Top,desc=Maximum active requests per sample; default 50"`
	MinElapsed string `json:"minElapsed,omitempty" clicky:"title=Minimum elapsed,desc=Only show requests running at least this long"`
}

func (o Options) Validate() error {
	if err := o.Selection.Validate(); err != nil {
		return err
	}
	if o.Poll != "" {
		if value, err := time.ParseDuration(o.Poll); err != nil || value <= 0 {
			return fmt.Errorf("poll must be a positive duration")
		}
	}
	if o.MinElapsed != "" {
		if value, err := time.ParseDuration(o.MinElapsed); err != nil || value < 0 {
			return fmt.Errorf("minElapsed must be a nonnegative duration")
		}
	}
	if o.Top < 0 || o.Top > 10000 {
		return fmt.Errorf("top must be between zero and 10000")
	}
	return nil
}

type Params struct {
	Options
	Target target.Target `json:"target,omitzero"`
}

type Sample struct {
	tracehttp.Metadata
	Source    string            `json:"source" filter:"terms"`
	SampledAt time.Time         `json:"sampledAt"`
	StartedAt time.Time         `json:"startedAt"`
	Method    string            `json:"method" filter:"terms"`
	Host      string            `json:"host" filter:"terms"`
	URL       string            `json:"url" filter:"text"`
	ElapsedMs float64           `json:"elapsedMs" pretty:"label=Elapsed,type=duration,unit=ms"`
	Resource  map[string]string `json:"resource,omitempty"`
}

type handler struct {
	agent seam.AgentSeam[Params, Sample]
}

func (handler) Params() Params {
	return Params{Options: Options{Poll: "5s", Top: 50, Selection: tracehttp.Selection{Direction: tracehttp.Both}}}
}
func (handler) Schema() recordresults.ResultType[Sample] {
	return ResultType()
}

func ResultType() recordresults.ResultType[Sample] {
	return recordresults.ResultType[Sample]{Kind: Kind, Title: "Active HTTP requests", TimeColumn: "sampledAt", Follow: true, SearchColumns: []string{"url", "route", "thread"}}
}
func (h handler) AgentAvailable(probe string, t target.Target) (bool, error) {
	return h.agent.Available(probe, t)
}
func (h handler) Prepare(ctx dbcontext.Context, p Params, out tracing.Emitter[Sample]) (dbcontext.Context, func() error, error) {
	return h.agent.Prepare(ctx, p, out)
}
func (h handler) Handle(ctx dbcontext.Context, p Params, out tracing.Emitter[Sample], _ tracing.Records[Sample]) error {
	return h.agent.Handle(ctx, p, out)
}
func (h handler) Checkpoint(ctx dbcontext.Context, _ Params, out tracing.Emitter[Sample]) error {
	var params struct {
		Reason string `json:"reason,omitempty"`
	}
	if _, err := tracing.DecodeCheckpointParams(ctx, &params); err != nil {
		return err
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return h.agent.Checkpoint(ctx, raw, out)
}

func NewKind(registry *agentclient.Registry) *tracing.Handler[Params, Sample] {
	agent := seam.AgentSeam[Params, Sample]{Probe: Kind, Registry: registry, Target: func(p Params) target.Target { return p.Target }, ArmParams: armParams, DecodeBlob: Decode}
	return tracing.NewHandler[Params, Sample](handler{agent: agent}, tracing.Capabilities{Live: true, Follow: true}, target.Agent).WithTop(tracing.TopSpec{Identity: []string{"source", "requestId"}, Order: query.Order{{Column: "elapsedMs", Desc: true}, {Column: "requestId"}}, PerSource: true})
}

func armParams(p Params) (json.RawMessage, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	interval := 5 * time.Second
	if p.Poll != "" {
		var err error
		interval, err = time.ParseDuration(p.Poll)
		if err != nil {
			return nil, err
		}
	}
	limit := p.Top
	if limit == 0 {
		limit = 50
	}
	var minimum time.Duration
	if p.MinElapsed != "" {
		var err error
		minimum, err = time.ParseDuration(p.MinElapsed)
		if err != nil {
			return nil, err
		}
	}
	return json.Marshal(struct {
		Options
		PollIntervalMs int64 `json:"pollIntervalMs"`
		Limit          int   `json:"limit"`
		MinElapsedNs   int64 `json:"minElapsedNs,omitempty"`
	}{Options: p.Options, PollIntervalMs: interval.Milliseconds(), Limit: limit, MinElapsedNs: int64(minimum)})
}
