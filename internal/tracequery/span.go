package tracequery

import (
	"strings"

	imported "github.com/flanksource/telltale/pkg/tracing/opensearch"
)

func fromImported(span imported.Span) Span {
	attributes := map[string]any{}
	resource := map[string]any{}
	for key, value := range span.Attributes {
		if name, ok := strings.CutPrefix(key, "resource."); ok {
			resource[strings.TrimPrefix(name, "attributes.")] = value
		} else {
			attributes[strings.TrimPrefix(key, "tag.")] = value
		}
	}
	if span.Service != "" {
		resource["service.name"] = span.Service
	}
	return Span{TraceID: span.TraceID, SpanID: span.SpanID, ParentID: span.ParentID, Service: span.Service, Operation: span.Operation, StartedAt: span.Timestamp, DurationMs: span.DurationMs, Status: span.Status, Attributes: attributes, Resource: resource}
}
