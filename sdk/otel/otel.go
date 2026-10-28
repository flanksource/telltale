package otel

import (
	"context"
	"fmt"
	"sync"
	"time"

	telltale "github.com/flanksource/telltale/sdk"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

var installation struct {
	sync.RWMutex
	provider *sdktrace.TracerProvider
}

func init() { telltale.RegisterSpanInstaller(install) }

func install() (func() error, error) {
	provider, ok := otel.GetTracerProvider().(*sdktrace.TracerProvider)
	if !ok {
		return nil, fmt.Errorf("spans: global OpenTelemetry tracer provider is not an SDK provider")
	}
	processor := &processor{automatic: true}
	installation.Lock()
	provider.RegisterSpanProcessor(processor)
	installation.provider = provider
	installation.Unlock()
	return func() error {
		installation.Lock()
		provider.UnregisterSpanProcessor(processor)
		installation.provider = nil
		installation.Unlock()
		return nil
	}, nil
}

type processor struct {
	automatic bool
	mu        sync.Mutex
	providers map[spanKey]trace.TracerProvider
}
type spanKey struct {
	trace trace.TraceID
	span  trace.SpanID
}

func key(sc trace.SpanContext) spanKey { return spanKey{sc.TraceID(), sc.SpanID()} }

// SpanProcessor can be installed on an application-owned tracer provider.
func SpanProcessor() sdktrace.SpanProcessor {
	return &processor{providers: make(map[spanKey]trace.TracerProvider)}
}

func (p *processor) OnStart(_ context.Context, span sdktrace.ReadWriteSpan) {
	if p.automatic {
		return
	}
	p.mu.Lock()
	p.providers[key(span.SpanContext())] = span.TracerProvider()
	p.mu.Unlock()
}
func (p *processor) OnEnd(span sdktrace.ReadOnlySpan) {
	if !p.automatic {
		p.mu.Lock()
		provider, observed := p.providers[key(span.SpanContext())]
		delete(p.providers, key(span.SpanContext()))
		p.mu.Unlock()
		installation.RLock()
		automatic := installation.provider != nil && installation.provider == provider
		installation.RUnlock()
		if !observed || automatic {
			return
		}
	}
	if !telltale.Armed("spans") {
		return
	}
	attrs := make(map[string]any, len(span.Attributes()))
	for _, attr := range span.Attributes() {
		attrs[string(attr.Key)] = attr.Value.AsInterface()
	}
	resource := make(map[string]string)
	for _, attr := range span.Resource().Attributes() {
		resource[string(attr.Key)] = attr.Value.Emit()
	}
	parent := ""
	if span.Parent().IsValid() {
		parent = span.Parent().SpanID().String()
	}
	telltale.Record(context.Background(), "spans", telltale.Span{TraceID: span.SpanContext().TraceID().String(), SpanID: span.SpanContext().SpanID().String(), ParentID: parent, Name: span.Name(), Kind: span.SpanKind().String(), StartedAt: span.StartTime(), DurationMs: float64(span.EndTime().Sub(span.StartTime())) / float64(time.Millisecond), Status: span.Status().Code.String(), Attrs: attrs, Resource: resource})
}
func (p *processor) Shutdown(context.Context) error {
	p.mu.Lock()
	clear(p.providers)
	p.mu.Unlock()
	return nil
}
func (*processor) ForceFlush(context.Context) error { return nil }
