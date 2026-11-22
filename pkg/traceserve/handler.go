package traceserve

import (
	"errors"
	"net/http"
	"strings"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/query/profilestore"
	"github.com/flanksource/telltale/pkg/sessions"
	"github.com/flanksource/telltale/pkg/tracing"
)

// Handler serves runtime's store as the sessions API in its read-only mode
// (sessions.ReadOnly), rooted at the path the host mounts it at (strip that
// prefix before it): the trace kinds, a stream's seq, window exports and the
// sessions of runtime's registry, each listed, read, followed and
// checkpointed. The host starts and stops the captures itself, so every start,
// stop, extend and restart is refused 403. Every other path is 404.
func Handler(runtime *tracing.Runtime) (http.Handler, error) {
	if runtime == nil || runtime.Results == nil || runtime.Sessions == nil {
		return nil, errors.New("serve trace store: an open runtime with a session registry is required")
	}
	service, err := sessions.New(sessions.Options{
		Profiles:  func() (profilestore.Store, error) { return runtime.Results.Registry, nil },
		Context:   func() dbcontext.Context { return dbcontext.New() },
		Registry:  runtime.Sessions,
		Store:     runtime.SessionStore,
		Records:   runtime.Results.Backend,
		Traces:    runtime,
		Authorize: sessions.ReadOnly,
	})
	if err != nil {
		return nil, err
	}
	handler, err := service.Handler(APIPrefix, http.NotFoundHandler())
	if err != nil || runtime.RecoverSessions == nil {
		return handler, err
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == APIPrefix+"/sessions" || strings.HasPrefix(r.URL.Path, APIPrefix+"/sessions/") {
			if err := runtime.RecoverSessions(r.Context()); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		handler.ServeHTTP(w, r)
	}), nil
}
