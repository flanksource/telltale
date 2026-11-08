package server

import (
	"net/http"
	"strings"

	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/traceserve"
)

func viewerNative(dataDir string, allowed map[string]bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, operation := r.PathValue("run"), r.PathValue("operation")
		if !allowed[id] || (operation != "seq" && operation != "export") {
			http.NotFound(w, r)
			return
		}
		runtime, closeStore, err := telltale.Reopen(dataDir, id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer func() {
			if closeStore() != nil {
				panic(http.ErrAbortHandler)
			}
		}()
		handler, err := traceserve.Handler(runtime)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		request := r.Clone(r.Context())
		request.URL.Path = strings.TrimPrefix(r.URL.Path, TracesMount+"/"+id)
		handler.ServeHTTP(w, request)
	})
}
