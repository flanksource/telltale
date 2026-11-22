package server

import (
	"github.com/flanksource/clicky/rpc"
	"github.com/flanksource/telltale/pkg/traces/read"
	"github.com/flanksource/telltale/pkg/tracing/processcapture"
)

func eventStream(description string) rpc.OpenAPIMediaType {
	return rpc.OpenAPIMediaType{Schema: &rpc.OpenAPISchema{Type: "string", Description: description}}
}

func followParam() rpc.OpenAPIParameter {
	return rpc.OpenAPIParameter{Name: "follow", In: "query", Schema: &rpc.OpenAPISchema{Type: "boolean"}, Description: "Stream updates over SSE"}
}

func addFeedPaths(spec *rpc.OpenAPISpec) {
	if spec.Paths == nil {
		spec.Paths = map[string]rpc.OpenAPIPath{}
	}
	addRecordsPath(spec)
	pageSchema := rpc.SchemaForStruct(read.FeedResult{})
	pageSchema.Properties["rows"].Items.Properties["ansi"] = &rpc.OpenAPISchema{Type: "string", Description: "Styled summary, included when presentation=ansi"}
	operation := rpc.OpenAPIOperation{Summary: "Read or follow a run's events", Description: "Top samples and their named results are excluded from the event feed. Read them through the capture's records endpoint with view=top.", Tags: []string{"Traces"}, Responses: map[string]rpc.OpenAPIResponse{
		"200": {Description: "Paged records, or a resumable live stream", Content: map[string]rpc.OpenAPIMediaType{
			"application/json":     {Schema: pageSchema},
			"application/x-ndjson": {Schema: &rpc.OpenAPISchema{Type: "string"}},
			"text/event-stream":    eventStream("Ordered records. Resume with the last event ID or the initial tail page's resume token."),
		}}, "400": {Description: "Invalid selection, ordering or cursor"},
	}}
	operation.Parameters = append(operation.Parameters, rpc.OpenAPIParameter{Name: "run", In: "path", Required: true, Schema: &rpc.OpenAPISchema{Type: "string"}})
	for _, name := range []string{"kind", "excludeKind", "presentation", "stream", "sort", "limit", "cursor", "follow", "format", "resume", "from", "until", "tail"} {
		operation.Parameters = append(operation.Parameters, rpc.OpenAPIParameter{Name: name, In: "query", Schema: &rpc.OpenAPISchema{Type: "string"}})
	}
	spec.Paths[TracesMount+"/{run}/records"] = rpc.OpenAPIPath{"get": operation}
	spec.Paths[TracesMount+"/{run}/processes"] = rpc.OpenAPIPath{"get": rpc.OpenAPIOperation{OperationID: "traceProcesses", Summary: "Latest measurements of each recorded process", Parameters: []rpc.OpenAPIParameter{{Name: "run", In: "path", Required: true, Schema: &rpc.OpenAPISchema{Type: "string"}}}, Responses: map[string]rpc.OpenAPIResponse{"200": {Description: "Process identities and latest recorded measurements", Content: map[string]rpc.OpenAPIMediaType{"application/json": {Schema: rpc.SchemaForStruct(read.ProcessSnapshot{})}}}, "400": {Description: "Invalid run or query"}, "500": {Description: "Process snapshot could not be read"}}}}
	spec.Paths[TracesMount+"/{run}/process/groups"] = rpc.OpenAPIPath{"get": rpc.OpenAPIOperation{OperationID: "recordedProcessGroups", Summary: "Derive complete process group frames from numeric observations", Parameters: []rpc.OpenAPIParameter{
		{Name: "run", In: "path", Required: true, Schema: &rpc.OpenAPISchema{Type: "string"}},
		{Name: "stream", In: "query", Required: true, Schema: &rpc.OpenAPISchema{Type: "string"}},
		{Name: "afterSeq", In: "query", Schema: &rpc.OpenAPISchema{Type: "integer"}},
		followParam(),
	}, Responses: map[string]rpc.OpenAPIResponse{"200": {Description: "Group frames derived from one atomic process metrics stream", Content: map[string]rpc.OpenAPIMediaType{"application/json": {Schema: rpc.SchemaForStruct(processcapture.Frames{})}}}, "400": {Description: "Invalid process capture or sequence"}}}}
	for path, description := range map[string]string{
		TracesMount + "/{run}/processes":      "Process snapshots, or process details with view=detail and stream set.",
		TracesMount + "/{run}/process/groups": "Resumable batches of complete process frames.",
		TracesMount:                           "Current list of runs and lifecycle changes.",
	} {
		methods := spec.Paths[path]
		operation, ok := methods["get"]
		if !ok {
			continue
		}
		if path != TracesMount+"/{run}/process/groups" {
			operation.Parameters = append(operation.Parameters, followParam())
		}
		success := operation.Responses["200"]
		if success.Content == nil {
			success.Content = map[string]rpc.OpenAPIMediaType{}
		}
		success.Content["text/event-stream"] = eventStream(description)
		operation.Responses["200"] = success
		methods["get"] = operation
	}
}
