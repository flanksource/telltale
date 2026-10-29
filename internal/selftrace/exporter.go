package selftrace

import (
	"context"

	"github.com/flanksource/commons/logger"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type reportingExporter struct {
	sdktrace.SpanExporter
	tracer *Tracer
}

func (e reportingExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	err := e.SpanExporter.ExportSpans(ctx, spans)
	if err != nil {
		e.tracer.mu.Lock()
		e.tracer.exportErr = err
		e.tracer.mu.Unlock()
		logger.Errorf("self-trace export failed for %d spans: %v", len(spans), err)
	}
	return err
}
