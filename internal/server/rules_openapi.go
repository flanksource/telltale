package server

import (
	"github.com/flanksource/clicky/rpc"
	"github.com/flanksource/telltale/internal/rules"
)

func addRulesPaths(spec *rpc.OpenAPISpec) {
	if spec.Paths == nil {
		spec.Paths = map[string]rpc.OpenAPIPath{}
	}
	schemas := map[string]*rpc.OpenAPISchema{"": {Type: "object", Properties: map[string]*rpc.OpenAPISchema{"config": rules.Schema(), "revision": {Type: "string"}, "bindings": {Type: "object"}, "schema": {Type: "object"}}}, "/status": rpc.SchemaForStruct(rules.Status{}), "/preview": {Type: "array", Items: rpc.SchemaForStruct(rules.Preview{})}, "/events": {Type: "array", Items: rpc.SchemaForStruct(rules.Event{})}}
	for suffix, schema := range schemas {
		operation := &rpc.OpenAPIOperation{Summary: "Read resource rules " + suffix, Tags: []string{"Rules"}, Responses: map[string]rpc.OpenAPIResponse{"200": {Description: "Resource rules result", Content: map[string]rpc.OpenAPIMediaType{"application/json": {Schema: schema}}}, "503": {Description: "Rules are not enabled"}, "400": {Description: "Invalid query or no snapshot available"}}}
		if suffix == "/events" {
			for _, name := range []string{"rule", "group", "incident", "since", "limit"} {
				operation.Parameters = append(operation.Parameters, rpc.OpenAPIParameter{Name: name, In: "query", Schema: &rpc.OpenAPISchema{Type: "string"}})
			}
		}
		spec.Paths[RulesMount+suffix] = rpc.OpenAPIPath{"get": *operation}
	}
}
