package records

import (
	"encoding/json"
	"net/http"

	"github.com/flanksource/telltale/pkg/traces/read"
)

func ProcessesHandler(dataDir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			http.Error(w, "process snapshot takes no query parameters", http.StatusBadRequest)
			return
		}
		selected, err := selectFeed(dataDir, r.PathValue("run"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer func() {
			if selected.Close() != nil {
				panic(http.ErrAbortHandler)
			}
		}()
		snapshot, err := read.Processes(r.Context(), selected.Refs)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if json.NewEncoder(w).Encode(snapshot) != nil {
			panic(http.ErrAbortHandler)
		}
	})
}
