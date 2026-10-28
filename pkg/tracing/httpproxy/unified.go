package httpproxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"time"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/recordstore/recordresults"
	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/httpx"
	"github.com/flanksource/telltale/pkg/tracing"
	tracehttp "github.com/flanksource/telltale/pkg/tracing/http"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

type UnifiedParams struct {
	Params
	Features      []string `json:"features,omitempty" clicky:"title=Features,desc=HTTP features to observe"`
	DedupWindow   string   `json:"dedupWindow,omitempty" clicky:"title=Dedup window,desc=Store identical exchanges only once within this duration"`
	dedupDuration time.Duration
}

var (
	ErrProxyFeatures      = errors.New("features cannot be used with the proxy seam")
	ErrInvalidDedupWindow = errors.New("invalid dedupWindow")
)

func (p UnifiedParams) httpParams() tracehttp.Params {
	return tracehttp.Params{Features: p.Features, DedupWindow: p.DedupWindow, Target: p.Target}
}

func (p *UnifiedParams) ValidateInput(raw json.RawMessage) error {
	if p.Target.Seam != target.Proxy || len(raw) == 0 {
		return nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if _, ok := fields["features"]; ok {
		return ErrProxyFeatures
	}
	return nil
}

func (p *UnifiedParams) Validate() error {
	p.dedupDuration = 0
	if p.DedupWindow != "" {
		value, err := time.ParseDuration(p.DedupWindow)
		if err != nil || value < 0 {
			return fmt.Errorf("%w %q: expected a non-negative duration such as 1h", ErrInvalidDedupWindow, p.DedupWindow)
		}
		p.dedupDuration = value
	}
	if p.Target.Seam == target.Proxy {
		return p.Params.Validate()
	}
	return p.httpParams().Validate()
}

type unified struct {
	self interface {
		tracing.TraceHandler[tracehttp.Params, tracehttp.Exchange]
		tracing.Preparer[tracehttp.Params, tracehttp.Exchange]
		AgentAvailable(string, target.Target) (bool, error)
	}
}

func (u unified) AgentAvailable(probe string, t target.Target) (bool, error) {
	return u.self.AgentAvailable(probe, t)
}
func (unified) Params() UnifiedParams {
	return UnifiedParams{Params: proxy{}.Params(), Features: []string{httpx.Feature}}
}
func (unified) Schema() recordresults.ResultType[tracehttp.Exchange] { return proxy{}.Schema() }
func (u unified) Prepare(ctx dbcontext.Context, p UnifiedParams, out tracing.Emitter[tracehttp.Exchange]) (dbcontext.Context, func() error, error) {
	if p.Target.Seam == target.Proxy {
		return (proxy{}).Prepare(ctx, p.Params, out)
	}
	return u.self.Prepare(ctx, p.httpParams(), out)
}
func (u unified) Handle(ctx dbcontext.Context, p UnifiedParams, out tracing.Emitter[tracehttp.Exchange], rows tracing.Records[tracehttp.Exchange]) error {
	if p.Target.Seam == target.Proxy {
		return (proxy{}).Handle(ctx, p.Params, out, rows)
	}
	return u.self.Handle(ctx, p.httpParams(), out, rows)
}
func (unified) Formats() []tracing.ExportFormat { return []tracing.ExportFormat{tracehttp.HAR} }
func (unified) Export(format string, rows iter.Seq[tracehttp.Exchange], w io.Writer) error {
	return tracehttp.Export(format, rows, w)
}
func (unified) Summarize(rows iter.Seq[tracehttp.Exchange]) (map[string]any, error) {
	return (proxy{}).Summarize(rows)
}

func UnifiedKind(registry *agentclient.Registry) *tracing.Handler[UnifiedParams, tracehttp.Exchange] {
	return tracing.NewHandler[UnifiedParams, tracehttp.Exchange](unified{self: tracehttp.NewCapture(registry)}, tracing.Capabilities{Live: true}, target.Self, target.Agent, target.Proxy).
		WithDeduplication(tracehttp.Key, func(p UnifiedParams) time.Duration { return p.dedupDuration }).
		WithSecretMasking().WithTruncation(maxValueBytes).WithJSONProcessor(tracehttp.RequestBody, tracehttp.ResponseBody)
}
