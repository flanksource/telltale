package server

import (
	"net/http"
	"strings"

	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/traceserve"
	"github.com/flanksource/telltale/pkg/tracing"
)

func viewerNative(dataDir string, allowed map[string]bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, operation := r.PathValue("run"), r.PathValue("operation")
		path := strings.TrimPrefix(r.URL.Path, TracesMount+"/"+id)
		if !allowed[id] || (path != "/api/v1/traces/kinds" && operation != "seq" && operation != "export") {
			http.NotFound(w, r)
			return
		}
		run, live, err := telltale.Live(dataDir, id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		var runtime *tracing.Runtime
		if live {
			runtime = run.Runtime()
		} else {
			stored, closeStore, err := telltale.Reopen(dataDir, id)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			defer func() {
				if closeStore() != nil {
					panic(http.ErrAbortHandler)
				}
			}()
			runtime = stored
		}
		handler, err := traceserve.Handler(runtime)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		request := r.Clone(r.Context())
		request.URL.Path = path
		handler.ServeHTTP(w, request)
	})
}
