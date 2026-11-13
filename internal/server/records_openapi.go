package server

import (
	"github.com/flanksource/clicky/rpc"
	"github.com/flanksource/commons-db/recordstore/sampleframe"
	"github.com/flanksource/telltale/pkg/traces/read"
)

func addRecordsPath(spec *rpc.OpenAPISpec) {
	operation := rpc.OpenAPIOperation{OperationID: "traceSampleRecords", Summary: "Read native history, completed samples, or a Top selection", Tags: []string{"Traces"}, Responses: map[string]rpc.OpenAPIResponse{
		"200": {Description: "Native history page, completed sample metadata, or Top response according to view", Content: map[string]rpc.OpenAPIMediaType{
			"application/json":        {Schema: &rpc.OpenAPISchema{OneOf: []*rpc.OpenAPISchema{rpc.SchemaForStruct(read.TopResult{}), {Type: "array", Items: rpc.SchemaForStruct(sampleframe.Frame{})}, {Type: "object", Properties: map[string]*rpc.OpenAPISchema{"rows": {Type: "array", Items: &rpc.OpenAPISchema{Type: "object"}}, "nextCursor": {Type: "string"}}}}}},
			"application/schema+json": {Schema: &rpc.OpenAPISchema{Type: "object", Description: "Options schema and native columns resolved for the selected view"}},
		}}, "400": {Description: "Invalid selection, filter, sort or cursor"}, "409": {Description: "Recording has no provable complete sample boundaries"},
	}}
	for _, name := range []string{"run", "kind"} {
		operation.Parameters = append(operation.Parameters, rpc.OpenAPIParameter{Name: name, In: "path", Required: true, Schema: &rpc.OpenAPISchema{Type: "string"}})
	}
	for _, name := range []string{"stream", "result", "view", "frame", "source", "metric", "q", "filter", "where", "sort", "desc", "limit", "cursor", "all", "from", "until", "afterSeq", "toSeq", "format", "follow", "stats", "groupBy"} {
		operation.Parameters = append(operation.Parameters, rpc.OpenAPIParameter{Name: name, In: "query", Schema: &rpc.OpenAPISchema{Type: "string"}})
	}
	spec.Paths[TracesMount+"/{run}/traces/{kind}/records"] = rpc.OpenAPIPath{"get": operation}
}
