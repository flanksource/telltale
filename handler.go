package telltale

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/flanksource/commons-db/connection/store"
	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/traceserve"
)

// maxStartBody bounds a start request.
const maxStartBody = 1 << 20

// stopWait bounds how long Close waits for the runs it stopped to finish.
const stopWait = 30 * time.Second

// HandlerOptions say which runs a Handler serves and where it is mounted.
type HandlerOptions struct {
	// WorkDir holds the run stores, under traces.Dir; new runs' commands run
	// in it.
	WorkDir string
	// Prefix is the path the handler is mounted at, such as /api/traces;
	// requests reach it with the prefix still on their path.
	Prefix string
	// Connections answer the connection references of the runs it starts,
	// and are what it lists at <prefix>/connections.
	Connections *store.Store
}

// RunsList is the runs a Handler lists, newest first.
type RunsList struct {
	Runs []MountedRun `json:"runs"`
}

// MountedRun is one run and where its store is served.
type MountedRun struct {
	RunInfo
	// Mount is <prefix>/<run id>, the root of the run's traceserve routes.
	Mount string `json:"mount"`
}

// StartForm is the start form's schema and the values it starts from.
type StartForm struct {
	Schema   any            `json:"schema"`
	Defaults map[string]any `json:"defaults"`
}

// Handler starts, stops, lists and serves the runs of a work dir, mounted at
// a prefix:
//
//	GET    <prefix>                 the runs, filtered by ?q, ?status, ?kind, ?since (RFC 3339), ?limit
//	POST   <prefix>                 start a run: a StartRequest JSON body, from the server's own origin
//	GET    <prefix>/start           the start form (StartForm)
//	GET    <prefix>/connections     the connections its runs can name (ConnectionInfo), of ?type alone
//	DELETE <prefix>/<run id>        stop a run this process is running
//	*      <prefix>/<run id>/...    the run's store (traceserve.Handler): live from its
//	                                running captures, else reopened read-only
//
// Runs it started stop when it is closed.
type Handler struct {
	options HandlerOptions
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	opened  map[string]openedStore
	started map[string]*Run
}

type openedStore struct {
	handler http.Handler
	close   func() error
}

// NewHandler serves the runs of options.WorkDir at options.Prefix.
func NewHandler(options HandlerOptions) (*Handler, error) {
	if options.WorkDir == "" || !strings.HasPrefix(options.Prefix, "/") || strings.HasSuffix(options.Prefix, "/") {
		return nil, fmt.Errorf("trace handler: a work dir and a prefix like /api/traces are required (got %q, %q)", options.WorkDir, options.Prefix)
	}
	if options.Connections == nil {
		return nil, fmt.Errorf("trace handler: a connection store is required, such as OpenConnections()")
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Handler{options: options, ctx: ctx, cancel: cancel, opened: map[string]openedStore{}, started: map[string]*Run{}}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	prefix := h.options.Prefix
	switch r.URL.Path {
	case prefix:
		h.serveRuns(w, r)
		return
	case prefix + "/start":
		h.serveStartForm(w, r)
		return
	case prefix + "/connections":
		h.serveConnections(w, r)
		return
	}
	runID, rest, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, prefix+"/"), "/")
	if !strings.HasPrefix(r.URL.Path, prefix+"/") || runID == "" || strings.Contains(runID, "..") {
		http.Error(w, "name a run: "+prefix+"/<run id>/...", http.StatusNotFound)
		return
	}
	if rest == "" && r.Method == http.MethodDelete {
		h.stop(w, r, runID)
		return
	}
	if rest == "" && !strings.HasSuffix(r.URL.Path, "/") {
		w.Header().Set("Location", runID+"/")
		w.WriteHeader(http.StatusMovedPermanently)
		return
	}
	handler, err := h.store(runID)
	switch {
	case errors.Is(err, os.ErrNotExist):
		http.Error(w, fmt.Sprintf("the trace store of run %s is gone: pruned, or never recorded", runID), http.StatusNotFound)
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	default:
		http.StripPrefix(prefix+"/"+runID, handler).ServeHTTP(w, r)
	}
}

func (h *Handler) serveRuns(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.list(w, r)
	case http.MethodPost:
		h.start(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	options, err := listOptions(h.options.WorkDir, r.URL.Query())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	runs, err := List(options)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	list := RunsList{Runs: make([]MountedRun, 0, len(runs))}
	for _, run := range runs {
		list.Runs = append(list.Runs, h.mounted(run))
	}
	writeJSON(w, http.StatusOK, list)
}

// listOptions reads a list's filters: q, status (repeated or comma
// separated), kind, since (RFC 3339) and limit.
func listOptions(workDir string, query url.Values) (ListOptions, error) {
	options := ListOptions{WorkDir: workDir, Query: query.Get("q"), Kind: query.Get("kind")}
	for _, values := range query["status"] {
		for _, value := range strings.Split(values, ",") {
			status, err := ParseStatus(value)
			if err != nil {
				return options, err
			}
			options.Status = append(options.Status, status)
		}
	}
	if since := query.Get("since"); since != "" {
		parsed, err := time.Parse(time.RFC3339, since)
		if err != nil {
			return options, fmt.Errorf("since: want an RFC 3339 time: %w", err)
		}
		options.Since = parsed
	}
	if limit := query.Get("limit"); limit != "" {
		parsed, err := strconv.Atoi(limit)
		if err != nil || parsed < 0 {
			return options, fmt.Errorf("limit: want a count of runs, got %q", limit)
		}
		options.Limit = parsed
	}
	return options, nil
}

func (h *Handler) mounted(run RunInfo) MountedRun {
	return MountedRun{RunInfo: run, Mount: h.options.Prefix + "/" + run.RunID}
}

func (h *Handler) serveStartForm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	schema, defaults, err := StartSchema()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, StartForm{Schema: schema, Defaults: defaults})
}

func (h *Handler) serveConnections(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	connections, err := ListConnections(h.options.Connections, r.URL.Query().Get("type"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string][]ConnectionInfo{"connections": connections})
}

// start runs the command a StartRequest names. It runs a shell command, so
// it takes only a JSON body from the server's own origin: a cross-site form
// cannot send one, and a cross-site script is held to a CORS preflight the
// handler never answers.
func (h *Handler) start(w http.ResponseWriter, r *http.Request) {
	if err := errors.Join(sameOrigin(r), jsonBody(r)); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	var request StartRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxStartBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, rootError("decode start request: "+err.Error()))
		return
	}
	options, err := request.Options(h.options.WorkDir)
	var invalid *ValidationError
	if errors.As(err, &invalid) {
		writeJSON(w, http.StatusBadRequest, invalid)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	options.Connections = h.options.Connections
	run, err := Start(h.ctx, options)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, rootError(err.Error()))
		return
	}
	h.track(run)
	writeJSON(w, http.StatusCreated, h.mounted(run.Info()))
}

// rootError refuses a start request as a whole, such as a capture that could
// not start, rather than one of its fields.
func rootError(message string) *ValidationError {
	return &ValidationError{Fields: []FieldError{{InstancePath: "", Message: message}}}
}

func jsonBody(r *http.Request) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return fmt.Errorf("send an application/json body, not %q", r.Header.Get("Content-Type"))
	}
	return nil
}

func sameOrigin(r *http.Request) error {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return nil
	}
	if parsed, err := url.Parse(origin); err != nil || parsed.Host != r.Host {
		return fmt.Errorf("start or stop a run from the server's own origin, not %q", origin)
	}
	return nil
}

// stop stops a run this process is running. DELETE is never a simple
// request, so a cross-site script is held to the preflight as a POST is.
func (h *Handler) stop(w http.ResponseWriter, r *http.Request, runID string) {
	if err := sameOrigin(r); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	err := Stop(StopOptions{WorkDir: h.options.WorkDir, RunID: runID})
	switch {
	case errors.Is(err, ErrNotLive):
		http.Error(w, err.Error(), http.StatusConflict)
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	default:
		w.WriteHeader(http.StatusAccepted)
	}
}

// track keeps a run the handler started until it is done, for Close to wait on.
func (h *Handler) track(run *Run) {
	h.mu.Lock()
	h.started[run.ID()] = run
	h.mu.Unlock()
	go func() {
		<-run.Done()
		h.mu.Lock()
		delete(h.started, run.ID())
		h.mu.Unlock()
	}()
}

// store is the handler of runID's store: the live run's own runtime while
// this process runs it, else the store reopened read-only.
func (h *Handler) store(runID string) (http.Handler, error) {
	run, ok, err := Live(h.options.WorkDir, runID)
	if err != nil {
		return nil, err
	}
	if ok {
		return traceserve.Handler(run.Runtime())
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if store, ok := h.opened[runID]; ok {
		return store.handler, nil
	}
	runtime, closeStore, err := traces.Reopen(traces.StoreDir(h.options.WorkDir, runID))
	if err != nil {
		return nil, err
	}
	handler, err := traceserve.Handler(runtime)
	if err != nil {
		return nil, errors.Join(err, closeStore())
	}
	h.opened[runID] = openedStore{handler: handler, close: closeStore}
	return handler, nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		panic(http.ErrAbortHandler)
	}
}

// Close stops the runs the handler started, waits for them to write their
// manifests, and closes every store it reopened.
func (h *Handler) Close() error {
	h.cancel()
	h.mu.Lock()
	running := make([]*Run, 0, len(h.started))
	for _, run := range h.started {
		running = append(running, run)
	}
	h.mu.Unlock()
	var errs []error
	deadline := time.After(stopWait)
	for _, run := range running {
		select {
		case <-run.Done():
			if _, err := run.Wait(); err != nil {
				errs = append(errs, err)
			}
		case <-deadline:
			errs = append(errs, fmt.Errorf("run %s did not finish within %s of the handler closing", run.ID(), stopWait))
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for runID, store := range h.opened {
		errs = append(errs, store.close())
		delete(h.opened, runID)
	}
	return errors.Join(errs...)
}
