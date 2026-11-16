package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/flanksource/clicky/rpc"
	"github.com/flanksource/telltale/internal/jvm/snapshot"
)

func jvmSnapshotHandler(w http.ResponseWriter, r *http.Request) {
	var options snapshot.Options
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&options); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if decoder.Decode(new(any)) != io.EOF || options.Agent == "" {
		http.Error(w, "one object with agent is required", http.StatusBadRequest)
		return
	}
	if err := options.Validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	result, err := snapshot.Collect(r.Context(), options)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	data, err := result.Zip()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	status := "complete"
	if !result.Manifest.Complete {
		status = "partial"
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"telltale-jvm-%s.zip\"", time.Now().UTC().Format("20060102T150405Z")))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Telltale-Snapshot-Status", status)
	if _, err := w.Write(data); err != nil {
		return
	}
}

func addJVMSnapshotPath(spec *rpc.OpenAPISpec) {
	// The binary download overrides the ordinary command executor at this path.
	if spec.Paths == nil {
		spec.Paths = map[string]rpc.OpenAPIPath{}
	}
	spec.Paths["/api/v1/jvm/snapshot"] = rpc.OpenAPIPath{"post": rpc.OpenAPIOperation{
		Summary: "Download a JVM support ZIP", OperationID: "jvmSnapshot",
		RequestBody: &rpc.OpenAPIRequestBody{Required: true, Content: map[string]rpc.OpenAPIMediaType{"application/json": {Schema: &rpc.OpenAPISchema{Type: "object", Required: []string{"agent"}, Properties: map[string]*rpc.OpenAPISchema{
			"agent": {Type: "string"}, "jfr": {Type: "string", Description: "Positive duration, at most 5m; omitted disables profiling"}, "heapHistogram": {Type: "boolean"},
		}}}}},
		Responses: map[string]rpc.OpenAPIResponse{"200": {Description: "ZIP; X-Telltale-Snapshot-Status is complete or partial", Content: map[string]rpc.OpenAPIMediaType{"application/zip": {Schema: &rpc.OpenAPISchema{Type: "string", Format: "binary"}}}}, "400": {Description: "Invalid request or unavailable Java target"}},
	}}
}
