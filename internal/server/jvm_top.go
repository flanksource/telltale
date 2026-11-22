package server

import (
	"github.com/flanksource/clicky/rpc"
	"github.com/flanksource/telltale/internal/jvm/top"
)

func addJVMTopPath(spec *rpc.OpenAPISpec) {
	path := spec.Paths["/api/v1/jvm/top"]
	operation, ok := path["post"]
	if !ok {
		return
	}
	operation.Summary = "Read one live JVM Top snapshot"
	operation.Responses["200"] = rpc.OpenAPIResponse{Description: "Clicky command response; output contains the JSON JVM Top frame", Content: map[string]rpc.OpenAPIMediaType{"application/json": {Schema: &rpc.OpenAPISchema{Type: "object", Properties: map[string]*rpc.OpenAPISchema{"success": {Type: "boolean"}, "output": {Type: "string", Description: "JSON-encoded JVM Top frame"}, "error": {Type: "string"}}}}}}
	if spec.Components == nil {
		spec.Components = &rpc.OpenAPIComponents{}
	}
	if spec.Components.Schemas == nil {
		spec.Components.Schemas = map[string]*rpc.OpenAPISchema{}
	}
	spec.Components.Schemas["JVMTopFrame"] = rpc.SchemaForStruct(top.Frame{})
	path["post"] = operation
	path["get"] = rpc.OpenAPIOperation{Summary: "Follow JVM Top snapshots", Tags: []string{"JVM"}, Parameters: []rpc.OpenAPIParameter{
		{Name: "agent", In: "query", Required: true, Schema: &rpc.OpenAPISchema{Type: "string"}},
	}, Responses: map[string]rpc.OpenAPIResponse{"200": {Description: "Live JVM Top frames", Content: map[string]rpc.OpenAPIMediaType{"text/event-stream": eventStream("A JSON JVMTopFrame on each sample.")}}}}
}
