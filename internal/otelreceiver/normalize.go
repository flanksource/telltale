package otelreceiver

import (
	otlpspans "github.com/flanksource/telltale/pkg/otlp/spans"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
)

func normalize(request ptraceotlp.ExportRequest, options Options) (batch, error) {
	converted, err := otlpspans.Normalize(request, otlpspans.Options{MaxSpans: options.MaxSpans, MaxRequestBytes: options.MaxRequestBytes})
	return batch{Spans: converted.Spans, Rejected: converted.Rejected, Reason: converted.Reason}, err
}
