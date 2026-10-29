package server

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/flanksource/clicky/rpc"
	"github.com/flanksource/commons-db/models"
	"github.com/flanksource/telltale/sdk/protocol"
)

func addConnectionPaths(spec *rpc.OpenAPISpec) {
	if spec.Paths == nil {
		spec.Paths = map[string]rpc.OpenAPIPath{}
	}
	for path, methods := range map[string][]string{
		"/api/v1/connection":                             {"GET", "POST"},
		"/api/v1/connection/{id}":                        {"GET", "PUT", "DELETE"},
		"/api/v1/connection/{id}/browser":                {"GET"},
		"/api/v1/connection/{id}/browser/inspect":        {"GET"},
		"/api/v1/connection/{id}/browser/query":          {"POST"},
		"/api/v1/connection/{id}/browser/filters/values": {"POST"},
		"/api/agents":                                    {"GET"},
		"/api/traces/{run}/traces/{kind}/records":        {"GET"},
	} {
		item := rpc.OpenAPIPath{}
		for _, method := range methods {
			status := http.StatusOK
			response := &rpc.OpenAPISchema{Type: "object"}
			if path == "/api/agents" {
				response = &rpc.OpenAPISchema{Type: "array", Items: rpc.SchemaForStruct(protocol.Hello{})}
			}
			if path == "/api/v1/connection" && method == "POST" {
				status = http.StatusCreated
			}
			if method == "DELETE" {
				status = http.StatusNoContent
			}
			op := &rpc.OpenAPIOperation{Summary: method + " " + path, Responses: map[string]rpc.OpenAPIResponse{
				strconv.Itoa(status): {Description: "Success", Content: map[string]rpc.OpenAPIMediaType{"application/json": {Schema: response}}},
				"400":                {Description: "Invalid request"}, "404": {Description: "Resource not found"}, "405": {Description: "Read-only resource"},
			}}
			for _, name := range []string{"id", "run", "kind"} {
				if strings.Contains(path, "{"+name+"}") {
					op.Parameters = append(op.Parameters, rpc.OpenAPIParameter{Name: name, In: "path", Required: true, Schema: &rpc.OpenAPISchema{Type: "string"}})
				}
			}
			if method == "POST" || method == "PUT" {
				body := &rpc.OpenAPISchema{Type: "object", AdditionalProperties: &rpc.OpenAPISchema{}}
				if path == "/api/v1/connection" || path == "/api/v1/connection/{id}" {
					body = rpc.SchemaForStruct(models.Connection{})
				}
				op.RequestBody = &rpc.OpenAPIRequestBody{Required: true, Content: map[string]rpc.OpenAPIMediaType{"application/json": {Schema: body}}}
			}
			item[strings.ToLower(method)] = *op
		}
		spec.Paths[path] = item
	}
}
