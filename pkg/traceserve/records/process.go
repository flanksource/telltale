package records

import (
	"encoding/json"
	"fmt"
	"github.com/flanksource/telltale/pkg/traces/read"
	"github.com/flanksource/telltale/pkg/tracing/processcapture"
	"net/http"
	"strconv"
)

func ProcessGroupsHandler(dataDir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for key := range r.URL.Query() {
			if key != "stream" && key != "afterSeq" {
				http.Error(w, "unknown process groups parameter: "+key, 400)
				return
			}
		}
		selected, err := selectRun(processcapture.Kind, read.Scope{DataDir: dataDir, Run: r.PathValue("run")})
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		defer func() {
			if selected.Close() != nil {
				panic(http.ErrAbortHandler)
			}
		}()
		refs, err := selectStreams(selected.Refs, r.URL.Query().Get("stream"))
		if err != nil || len(refs) != 1 {
			http.Error(w, fmt.Sprintf("select one process capture: %v", err), 400)
			return
		}
		after := int64(0)
		if value := r.URL.Query().Get("afterSeq"); value != "" {
			after, err = strconv.ParseInt(value, 10, 64)
		}
		if err != nil || after < 0 {
			http.Error(w, "invalid afterSeq", 400)
			return
		}
		ref := refs[0].TraceRef
		ref.AfterSeq = after
		frames, err := processcapture.ReadFrames(r.Context(), refs[0].Runtime, ref)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if json.NewEncoder(w).Encode(frames) != nil {
			panic(http.ErrAbortHandler)
		}
	})
}
