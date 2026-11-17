package server

import (
	"maps"

	"github.com/flanksource/clicky/rpc"
)

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
	profileContent := map[string]rpc.OpenAPIMediaType{
		"application/x-protobuf": {Schema: &rpc.OpenAPISchema{Type: "string", Format: "binary"}},
		"application/json":       {Schema: &rpc.OpenAPISchema{Type: "object", Description: "OTLP ExportProfilesServiceRequest with resource profiles and dictionary references"}},
	}
	profileResponses := maps.Clone(responses)
	for code, response := range profileResponses {
		response.Content = profileContent
		profileResponses[code] = response
	}
	profileResponses["200"] = rpc.OpenAPIResponse{Description: "Atomically committed profile snapshots; partialSuccess reports rejected samples", Content: profileContent}
	profileResponses["413"] = rpc.OpenAPIResponse{Description: "Profile request or sample limit exceeded", Content: profileContent}
	spec.Paths["/v1/profiles"] = rpc.OpenAPIPath{"post": {
		Summary: "Receive OTLP active-operation Profiles (serve --otlp)", Tags: []string{"OpenTelemetry"},
		Description: "Stores complete HTTP, SQL and JMS handler top snapshots in the receiver source. Capture attributes do not authorize writes into active runs. Supports gzip and requires the compiled Profiles development version.",
		Parameters:  []rpc.OpenAPIParameter{{Name: "OTLP-Profiles-Development-Version", In: "header", Required: true, Schema: &rpc.OpenAPISchema{Type: "string", Description: "Profiles development version; currently 1"}}},
		RequestBody: &rpc.OpenAPIRequestBody{Required: true, Content: profileContent}, Responses: profileResponses,
	}}
}
