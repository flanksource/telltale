package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/flanksource/telltale/internal/tracerun"
	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/traceserve"
)

// TracesMount is where the server serves the run stores of its work dir, each
// at TracesMount/<run id> (traceserve.Handler), and their list at TracesMount.
const TracesMount = "/api/traces"

// TracesStatus is the run stores the server can serve, newest first.
type TracesStatus struct {
	Runs []RunTraces `json:"runs"`
}

// RunTraces is one run's store as the UI reaches it.
type RunTraces struct {
	RunID string `json:"runId"`
	// Mount is TracesMount/<run id>.
	Mount string `json:"mount"`
	// Run is what the run recorded, from its manifest; nil when the run did
	// not finish, and Unavailable says why.
	Run         *tracerun.Result `json:"run,omitempty"`
	Unavailable string           `json:"unavailable,omitempty"`
}

// TraceMount serves the stores `telltale trace run` left under a work dir,
// reopening each read-only the first time it is asked for and keeping it open
// until Close.
type TraceMount struct {
	workDir string
	mu      sync.Mutex
	opened  map[string]openedStore
}

type openedStore struct {
	handler http.Handler
	close   func() error
}

func NewTraceMount(workDir string) *TraceMount {
	return &TraceMount{workDir: workDir, opened: map[string]openedStore{}}
}

// Status lists the run stores under the work dir; one that never traced a run
// has none.
func (m *TraceMount) Status() (TracesStatus, error) {
	entries, err := os.ReadDir(filepath.Join(m.workDir, traces.Dir))
	if errors.Is(err, os.ErrNotExist) {
		return TracesStatus{Runs: []RunTraces{}}, nil
	}
	if err != nil {
		return TracesStatus{}, fmt.Errorf("list trace stores: %w", err)
	}
	runs := []RunTraces{}
	for _, entry := range slices.Backward(entries) {
		if !entry.IsDir() {
			continue
		}
		run := RunTraces{RunID: entry.Name(), Mount: TracesMount + "/" + entry.Name()}
		manifest, err := tracerun.ReadManifest(traces.StoreDir(m.workDir, entry.Name()))
		switch {
		case errors.Is(err, os.ErrNotExist):
			run.Unavailable = "the run did not finish: it left no " + tracerun.ManifestFile
		case err != nil:
			return TracesStatus{}, err
		default:
			run.Run = manifest
		}
		runs = append(runs, run)
	}
	return TracesStatus{Runs: runs}, nil
}

func (m *TraceMount) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == TracesMount {
		m.serveStatus(w, r)
		return
	}
	runID, rest, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, TracesMount+"/"), "/")
	if runID == "" || strings.Contains(runID, "..") {
		http.Error(w, "name a run: "+TracesMount+"/<run id>/...", http.StatusNotFound)
		return
	}
	if rest == "" && !strings.HasSuffix(r.URL.Path, "/") {
		w.Header().Set("Location", runID+"/")
		w.WriteHeader(http.StatusMovedPermanently)
		return
	}
	store, err := m.store(runID)
	switch {
	case errors.Is(err, os.ErrNotExist):
		http.Error(w, fmt.Sprintf("the trace store of run %s is gone: pruned, or never recorded", runID), http.StatusNotFound)
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	default:
		http.StripPrefix(TracesMount+"/"+runID, store.handler).ServeHTTP(w, r)
	}
}

func (m *TraceMount) serveStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	status, err := m.Status()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, status)
}

func (m *TraceMount) store(runID string) (openedStore, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if store, ok := m.opened[runID]; ok {
		return store, nil
	}
	runtime, closeStore, err := traces.Reopen(traces.StoreDir(m.workDir, runID))
	if err != nil {
		return openedStore{}, err
	}
	handler, err := traceserve.Handler(runtime)
	if err != nil {
		return openedStore{}, errors.Join(err, closeStore())
	}
	store := openedStore{handler: handler, close: closeStore}
	m.opened[runID] = store
	return store, nil
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		panic(http.ErrAbortHandler)
	}
}

// Close closes every store the mount reopened.
func (m *TraceMount) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var errs []error
	for runID, store := range m.opened {
		errs = append(errs, store.close())
		delete(m.opened, runID)
	}
	return errors.Join(errs...)
}
