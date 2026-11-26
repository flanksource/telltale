package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/flanksource/clicky/sse"
	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/traceserve/records"
	"github.com/flanksource/telltale/webapp"
)

type Viewer struct {
	closeOnce sync.Once
	closeErr  error
	URL       string
	listener  net.Listener
	server    *http.Server
	cancel    context.CancelFunc
	done      chan error
}

type ViewerOptions struct {
	DataDir string
	RunIDs  []string
}

// StartViewer exposes only the selected runs' records and the shared frontend.
func StartViewer(options ViewerOptions) (*Viewer, error) {
	if len(options.RunIDs) == 0 {
		return nil, fmt.Errorf("trace viewer requires at least one run ID")
	}
	allowed := map[string]bool{}
	for _, id := range options.RunIDs {
		if allowed[id] {
			return nil, fmt.Errorf("run %q selected twice", id)
		}
		info, err := telltale.Get(telltale.GetOptions{DataDir: options.DataDir, RunID: id})
		if err != nil {
			return nil, err
		}
		if info.Unavailable != "" {
			return nil, fmt.Errorf("view run %s: %s", id, info.Unavailable)
		}
		allowed[id] = true
	}
	assets, err := webapp.Assets()
	if err != nil {
		return nil, err
	}
	if _, err := fs.ReadFile(assets, "index.html"); err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	feed := records.FeedHandler(options.DataDir)
	mux.HandleFunc("GET /api/traces/{run}/records", func(w http.ResponseWriter, r *http.Request) {
		if !allowed[r.PathValue("run")] {
			http.NotFound(w, r)
			return
		}
		feed.ServeHTTP(w, r)
	})
	processes := records.ProcessesHandler(options.DataDir)
	groups := records.ProcessGroupsHandler(options.DataDir)
	mux.HandleFunc("GET /api/traces/{run}/processes", func(w http.ResponseWriter, r *http.Request) {
		if !allowed[r.PathValue("run")] {
			http.NotFound(w, r)
			return
		}
		processes.ServeHTTP(w, r)
	})
	mux.HandleFunc("GET /api/traces/{run}/process/groups", func(w http.ResponseWriter, r *http.Request) {
		if !allowed[r.PathValue("run")] {
			http.NotFound(w, r)
			return
		}
		groups.ServeHTTP(w, r)
	})
	native := records.Handler(options.DataDir)
	nativeHandler := viewerNative(options.DataDir, allowed)
	mux.Handle("GET /api/traces/{run}/api/v1/traces/kinds", nativeHandler)
	mux.Handle("GET /api/traces/{run}/api/v1/traces/{kind}/{operation}", nativeHandler)
	mux.HandleFunc("GET /api/traces/{run}/traces/{kind}/records", func(w http.ResponseWriter, r *http.Request) {
		if !allowed[r.PathValue("run")] {
			http.NotFound(w, r)
			return
		}
		native.ServeHTTP(w, r)
	})
	mux.HandleFunc("GET /api/traces", func(w http.ResponseWriter, r *http.Request) {
		read := func() (telltale.RunsList, error) {
			runs := make([]telltale.MountedRun, 0, len(options.RunIDs))
			for _, id := range options.RunIDs {
				info, err := telltale.Get(telltale.GetOptions{DataDir: options.DataDir, RunID: id})
				if err != nil {
					return telltale.RunsList{}, err
				}
				runs = append(runs, telltale.MountedRun{RunInfo: info, Mount: TracesMount + "/" + id})
			}
			return telltale.RunsList{Runs: runs}, nil
		}
		if r.URL.Query().Get("follow") == "1" {
			telltale.ServeRunUpdates(w, r, options.DataDir, options.RunIDs, read)
			return
		}
		runs, err := read()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(runs); err != nil {
			panic(http.ErrAbortHandler)
		}
	})
	mux.HandleFunc("/api/", http.NotFound)
	mux.Handle("/", assetHandler(assets))
	events := sse.NewHub(sse.HubOptions{Build: "telltale-viewer"})
	handler := events.Guard(mux)
	events.Register(mux, handler)
	params := url.Values{"viewer": {"1"}, "run": {options.RunIDs[0]}}
	ctx, cancel := context.WithCancel(context.Background())
	viewer := &Viewer{URL: "http://" + listener.Addr().String() + "/?" + params.Encode(), listener: listener, cancel: cancel,
		server: &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: time.Minute,
			BaseContext: func(net.Listener) context.Context { return ctx }}, done: make(chan error, 1),
	}
	go func() {
		err := viewer.server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		viewer.done <- err
	}()
	return viewer, nil
}

func (v *Viewer) Close(ctx context.Context) error {
	v.closeOnce.Do(func() {
		v.cancel()
		v.closeErr = v.server.Shutdown(ctx)
		if v.closeErr != nil {
			v.closeErr = errors.Join(v.closeErr, v.server.Close())
		}
		v.closeErr = errors.Join(v.closeErr, <-v.done)
	})
	return v.closeErr
}
