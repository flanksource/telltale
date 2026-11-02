package server

import "github.com/flanksource/clicky/rpc"

func addReceiverPath(spec *rpc.OpenAPISpec) {
	if spec.Paths == nil {
		spec.Paths = map[string]rpc.OpenAPIPath{}
	}
	content := map[string]rpc.OpenAPIMediaType{
		"application/x-protobuf": {Schema: &rpc.OpenAPISchema{Type: "string", Format: "binary"}},
		"application/json":       {Schema: &rpc.OpenAPISchema{Type: "object", Description: "Standard OTLP JSON with hexadecimal trace and span IDs"}},
	}
	responses := map[string]rpc.OpenAPIResponse{}
	for code, description := range map[string]string{"200": "Committed export; partialSuccess reports rejected spans", "400": "Malformed OTLP request", "401": "API token required", "403": "API scope required", "413": "Request or span limit exceeded", "415": "Unsupported content type", "503": "Storage unavailable or receiver at capacity; retry later"} {
		responses[code] = rpc.OpenAPIResponse{Description: description, Content: content}
	}
	spec.Paths["/v1/traces"] = rpc.OpenAPIPath{"post": {
		Summary: "Receive OTLP traces (serve --otlp)", Tags: []string{"OpenTelemetry"},
		Description: "Continuously persists traces without arming. Non-loopback HTTP listeners require an API-scoped bearer token. Supports gzip. Responses use the request encoding; errors are google.rpc.Status.",
		RequestBody: &rpc.OpenAPIRequestBody{Required: true, Content: content}, Responses: responses,
	}}
}
