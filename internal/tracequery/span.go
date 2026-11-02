package tracequery

import (
	"github.com/flanksource/telltale/internal/otelreceiver"
	sdk "github.com/flanksource/telltale/sdk"
)

// fromSpan is the query view of a stored span, whichever source recorded it.
// A received span's attributes and resource come from its native OTLP
// request, which keeps them exact, and withOTLP adds that request itself.
func fromSpan(span sdk.Span, withOTLP bool) (Span, error) {
	span, err := otelreceiver.RestoreNative(span)
	if err != nil {
		return Span{}, err
	}
	result := Span{
		TraceID: span.TraceID, SpanID: span.SpanID, ParentID: span.ParentID, Service: span.Service, Operation: span.Operation,
		Kind: span.Kind, StartedAt: span.StartedAt, DurationMs: span.DurationMs, Status: span.Status,
		Attributes: span.Attributes, Resource: span.Resource,
	}
	if withOTLP && span.Native != "" {
		if result.OTLP, err = otelreceiver.NativeJSON(span); err != nil {
			return Span{}, err
		}
	}
	return result, nil
}
