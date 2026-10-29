package server

import (
	"github.com/flanksource/clicky/rpc"
	"github.com/flanksource/telltale/internal/tracequery"
)

func addOtelPaths(spec *rpc.OpenAPISpec) {
	if spec.Paths == nil {
		spec.Paths = map[string]rpc.OpenAPIPath{}
	}
	request := rpc.SchemaForStruct(tracequery.Request{})
	for suffix, response := range map[string]*rpc.OpenAPISchema{"": rpc.SchemaForStruct(tracequery.SearchResult{}), "/trace/{id}": rpc.SchemaForStruct(tracequery.Detail{}), "/sources": {Type: "array", Items: rpc.SchemaForStruct(tracequery.Source{})}, "/form": {Type: "object"}} {
		op := rpc.OpenAPIOperation{Summary: "OpenTelemetry trace search " + suffix, Tags: []string{"OpenTelemetry"}, Responses: map[string]rpc.OpenAPIResponse{"200": {Description: "Trace result", Content: map[string]rpc.OpenAPIMediaType{"application/json": {Schema: response}}}, "400": {Description: "Invalid search or unavailable source"}, "404": {Description: "Trace not found"}}}
		if suffix == "" || suffix == "/trace/{id}" {
			for name, schema := range request.Properties {
				if name == "tags" {
					schema = &rpc.OpenAPISchema{Type: "string", Description: "JSON object of exact span or resource attribute values"}
				}
				op.Parameters = append(op.Parameters, rpc.OpenAPIParameter{Name: name, In: "query", Required: name == "source", Schema: schema})
			}
		}
		if suffix == "/trace/{id}" {
			response.Properties["spans"].Items.Properties["otlp"] = &rpc.OpenAPISchema{Type: "object", Description: "Redacted native OTLP ExportTraceServiceRequest for this span, including its resource and scope"}
			for _, field := range []string{"attributes", "resource"} {
				response.Properties["spans"].Items.Properties[field].AdditionalProperties = &rpc.OpenAPISchema{}
			}
			op.Parameters = append(op.Parameters, rpc.OpenAPIParameter{Name: "id", In: "path", Required: true, Schema: &rpc.OpenAPISchema{Type: "string"}})
		}
		spec.Paths[otelPath+suffix] = rpc.OpenAPIPath{"get": op}
	}
}
