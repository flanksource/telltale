package records

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/flanksource/telltale/pkg/traces/read"
)

func serveTop(w http.ResponseWriter, r *http.Request, refs []read.Ref, q read.Query, view string) {
	if len(refs) != 1 {
		http.Error(w, "Top requires one capture stream; specify stream", http.StatusBadRequest)
		return
	}
	var result any
	var err error
	if view == "frames" {
		result, err = read.Frames(r.Context(), refs[0])
	} else {
		values := r.URL.Query()
		result, err = read.Top(r.Context(), refs[0], read.TopQuery{Frame: values.Get("frame"), Source: values.Get("source"), Metric: values.Get("metric"), Query: q})
	}
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, read.ErrNoCompleteSamples) {
			status = http.StatusConflict
		}
		http.Error(w, err.Error(), status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(result); err != nil {
		panic(http.ErrAbortHandler)
	}
}
