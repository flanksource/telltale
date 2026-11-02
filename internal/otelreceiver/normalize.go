package otelreceiver

import (
	"encoding/base64"
	"fmt"
	"math"
	"time"

	"github.com/flanksource/telltale/pkg/tracing"
	sdk "github.com/flanksource/telltale/sdk"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
)

type spanContext struct {
	resource ptrace.ResourceSpans
	scope    ptrace.ScopeSpans
	span     ptrace.Span
}

func normalize(request ptraceotlp.ExportRequest, options Options) (batch, error) {
	result := batch{Spans: []sdk.Span{}}
	if request.Traces().SpanCount() > options.MaxSpans {
		return result, fmt.Errorf("%w: at most %d spans", ErrLimit, options.MaxSpans)
	}
	size := 0
	resources := request.Traces().ResourceSpans()
	for i := 0; i < resources.Len(); i++ {
		resource := resources.At(i)
		for j := 0; j < resource.ScopeSpans().Len(); j++ {
			scope := resource.ScopeSpans().At(j)
			for k := 0; k < scope.Spans().Len(); k++ {
				span, err := convertSpan(spanContext{resource: resource, scope: scope, span: scope.Spans().At(k)})
				if err != nil {
					result.Rejected++
					result.Reason = err.Error()
					continue
				}
				size += len(span.Native)
				if size > options.MaxRequestBytes*8 {
					return batch{}, fmt.Errorf("%w: expanded span contexts exceed %d bytes; reduce the batch size", ErrLimit, options.MaxRequestBytes*8)
				}
				result.Spans = append(result.Spans, span)
			}
		}
	}
	return result, nil
}

func convertSpan(input spanContext) (sdk.Span, error) {
	span := input.span
	if span.TraceID().IsEmpty() || span.SpanID().IsEmpty() || span.StartTimestamp() == 0 || span.EndTimestamp() < span.StartTimestamp() || uint64(span.EndTimestamp()) > math.MaxInt64 || span.SpanID() == span.ParentSpanID() {
		return sdk.Span{}, fmt.Errorf("invalid span: nonzero trace/span IDs, valid timestamps and a distinct parent are required")
	}
	native := ptraceotlp.NewExportRequest()
	resource := native.Traces().ResourceSpans().AppendEmpty()
	input.resource.Resource().CopyTo(resource.Resource())
	resource.SetSchemaUrl(input.resource.SchemaUrl())
	scope := resource.ScopeSpans().AppendEmpty()
	input.scope.Scope().CopyTo(scope.Scope())
	scope.SetSchemaUrl(input.scope.SchemaUrl())
	span.CopyTo(scope.Spans().AppendEmpty())
	span = scope.Spans().At(0)
	if err := redact(spanContext{resource: resource, scope: scope, span: span}); err != nil {
		return sdk.Span{}, err
	}
	encoded, err := native.MarshalProto()
	if err != nil {
		return sdk.Span{}, fmt.Errorf("encode OTLP span: %w", err)
	}
	jsonData, err := native.MarshalJSON()
	if err != nil {
		return sdk.Span{}, err
	}
	digest, err := nativeDigest(jsonData)
	if err != nil {
		return sdk.Span{}, err
	}
	parent := ""
	if !span.ParentSpanID().IsEmpty() {
		parent = span.ParentSpanID().String()
	}
	record := sdk.Span{
		TraceID: span.TraceID().String(), SpanID: span.SpanID().String(), ParentID: parent,
		Operation: span.Name(), Kind: span.Kind().String(),
		StartedAt:  span.StartTimestamp().AsTime(),
		DurationMs: float64(span.EndTimestamp()-span.StartTimestamp()) / float64(time.Millisecond),
		Status:     span.Status().Code().String(),
		Attributes: span.Attributes().AsRaw(), Resource: resource.Resource().Attributes().AsRaw(),
		Native: base64.StdEncoding.EncodeToString(encoded), Digest: digest,
		Source: sdk.SpanSource{Type: sdk.SpanSourceOTLP},
	}
	if err := record.Normalize(); err != nil {
		return sdk.Span{}, fmt.Errorf("invalid span: %w", err)
	}
	return record, nil
}

func redact(input spanContext) error {
	span := input.span
	maps := []pcommon.Map{input.resource.Resource().Attributes(), input.scope.Scope().Attributes(), span.Attributes()}
	for i := 0; i < span.Events().Len(); i++ {
		event := span.Events().At(i)
		event.SetName(sdk.Mask(event.Name()))
		maps = append(maps, event.Attributes())
	}
	for i := 0; i < span.Links().Len(); i++ {
		maps = append(maps, span.Links().At(i).Attributes())
	}
	for _, attributes := range maps {
		values := attributes.AsRaw()
		if len(values) != attributes.Len() {
			return fmt.Errorf("invalid span: duplicate attribute keys")
		}
		for key, value := range values {
			values[key] = tracing.MaskAttribute(key, value)
		}
		if err := attributes.FromRaw(values); err != nil {
			return fmt.Errorf("redact OTLP attributes: %w", err)
		}
	}
	span.SetName(sdk.Mask(span.Name()))
	span.Status().SetMessage(sdk.Mask(span.Status().Message()))
	return nil
}
