package server

import (
	"github.com/flanksource/clicky/rpc"
	"github.com/flanksource/telltale/pkg/traces/read"
	"github.com/flanksource/telltale/pkg/tracing/processcapture"
)

func addFeedPaths(spec *rpc.OpenAPISpec) {
	if spec.Paths == nil {
		spec.Paths = map[string]rpc.OpenAPIPath{}
	}
	pageSchema := rpc.SchemaForStruct(read.FeedResult{})
	pageSchema.Properties["rows"].Items.Properties["ansi"] = &rpc.OpenAPISchema{Type: "string", Description: "Styled summary, included when presentation=ansi"}
	operation := rpc.OpenAPIOperation{Summary: "Read or follow every armed capture of a run", Tags: []string{"Traces"}, Responses: map[string]rpc.OpenAPIResponse{
		"200": {Description: "Paged records, or a resumable live stream", Content: map[string]rpc.OpenAPIMediaType{
			"application/json":     {Schema: pageSchema},
			"application/x-ndjson": {Schema: &rpc.OpenAPISchema{Type: "string"}},
			"text/event-stream":    {Schema: &rpc.OpenAPISchema{Type: "string"}},
		}}, "400": {Description: "Invalid selection, ordering or cursor"},
	}}
	operation.Parameters = append(operation.Parameters, rpc.OpenAPIParameter{Name: "run", In: "path", Required: true, Schema: &rpc.OpenAPISchema{Type: "string"}})
	for _, name := range []string{"kind", "excludeKind", "presentation", "stream", "sort", "limit", "cursor", "follow", "format", "resume", "from", "until"} {
		operation.Parameters = append(operation.Parameters, rpc.OpenAPIParameter{Name: name, In: "query", Schema: &rpc.OpenAPISchema{Type: "string"}})
	}
	spec.Paths[TracesMount+"/{run}/records"] = rpc.OpenAPIPath{"get": operation}
	spec.Paths[TracesMount+"/{run}/processes"] = rpc.OpenAPIPath{"get": rpc.OpenAPIOperation{OperationID: "traceProcesses", Summary: "Latest measurements of each recorded process", Parameters: []rpc.OpenAPIParameter{{Name: "run", In: "path", Required: true, Schema: &rpc.OpenAPISchema{Type: "string"}}}, Responses: map[string]rpc.OpenAPIResponse{"200": {Description: "Process identities and latest recorded measurements", Content: map[string]rpc.OpenAPIMediaType{"application/json": {Schema: rpc.SchemaForStruct(read.ProcessSnapshot{})}}}, "400": {Description: "Invalid run or query"}, "500": {Description: "Process snapshot could not be read"}}}}
	spec.Paths[TracesMount+"/{run}/process/groups"] = rpc.OpenAPIPath{"get": rpc.OpenAPIOperation{OperationID: "recordedProcessGroups", Summary: "Derive complete process group frames from numeric observations", Parameters: []rpc.OpenAPIParameter{
		{Name: "run", In: "path", Required: true, Schema: &rpc.OpenAPISchema{Type: "string"}},
		{Name: "stream", In: "query", Required: true, Schema: &rpc.OpenAPISchema{Type: "string"}},
		{Name: "afterSeq", In: "query", Schema: &rpc.OpenAPISchema{Type: "integer"}},
	}, Responses: map[string]rpc.OpenAPIResponse{"200": {Description: "Group frames derived from one atomic process metrics stream", Content: map[string]rpc.OpenAPIMediaType{"application/json": {Schema: rpc.SchemaForStruct(processcapture.Frames{})}}}, "400": {Description: "Invalid process capture or sequence"}}}}
}
