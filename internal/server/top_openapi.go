package server

import (
	"github.com/flanksource/clicky/rpc"
)

func addTopPaths(spec *rpc.OpenAPISpec) {
	if spec.Paths == nil {
		spec.Paths = map[string]rpc.OpenAPIPath{}
	}
	paths := []struct {
		path, summary string
		schema        *rpc.OpenAPISchema
		filters       []rpc.OpenAPIParameter
	}{
		{TopMount, "Read live host process groups", rpc.SchemaForStruct(TopFrame{}), nil},
		{TopProcessesPath, "Read live processes matching exactly one filter", rpc.SchemaForStruct(TopProcesses{}), []rpc.OpenAPIParameter{
			{Name: "name", In: "query", Schema: &rpc.OpenAPISchema{Type: "string"}},
			{Name: "pid", In: "query", Schema: &rpc.OpenAPISchema{Type: "integer"}},
			{Name: "q", In: "query", Schema: &rpc.OpenAPISchema{Type: "string"}},
		}},
	}
	for _, path := range paths {
		spec.Paths[path.path] = rpc.OpenAPIPath{"get": {
			Summary:     path.summary,
			Description: "Samples on demand when the shared rolling CPU budget permits. Poll no faster than polling.minimumIntervalMs.",
			Tags:        []string{"Top"},
			Parameters: append(path.filters,
				rpc.OpenAPIParameter{Name: "all", In: "query", Description: "Include processes outside tool groups", Schema: &rpc.OpenAPISchema{Type: "boolean"}},
				rpc.OpenAPIParameter{Name: "net", In: "query", Description: "Sample network counters for active flows on macOS", Schema: &rpc.OpenAPISchema{Type: "boolean"}}),
			Responses: map[string]rpc.OpenAPIResponse{
				"200": {Description: "Latest sample and polling period", Content: map[string]rpc.OpenAPIMediaType{"application/json": {Schema: path.schema}}},
				"400": {Description: "Invalid query"},
				"501": {Description: "Network sampling is unavailable on this platform"},
			},
		}}
	}
}
