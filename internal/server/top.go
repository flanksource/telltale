package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/flanksource/telltale/pkg/tracing/groups"
	"github.com/flanksource/telltale/pkg/tracing/process"
)

// TopMount serves the latest frame of the host's process groups, read live
// and kept nowhere: GET with all=true to include the processes no tool's
// group holds, net=true to read network bytes with nettop. Under it,
// TopProcessesPath serves the processes running a program (name=) or a
// process and those under it (pid=).
const TopMount = "/api/top"

// TopProcessesPath is the processes route under TopMount.
const TopProcessesPath = TopMount + "/processes"

// TopFacetsPath serves the facets (groups.Facets) of a process the latest
// frame listed (pid=): its context, environment and open descriptors.
const TopFacetsPath = TopMount + "/facets"

// TopProcesses are the processes of the latest frame a filter picks, with
// the host's use at it.
type TopProcesses struct {
	SampledAt time.Time              `json:"sampledAt"`
	Tick      int64                  `json:"tick"`
	Host      *groups.HostUsage      `json:"host,omitempty"`
	Processes []groups.ProcessSample `json:"processes"`
}

// topIdle is how long a live Top keeps reading after it was last asked for a
// frame.
const topIdle = 30 * time.Second

// TopHandler serves TopMount, one Top per choice of all and net, each read
// every groups.DefaultInterval while asked for.
type TopHandler struct {
	workDir string
	sampler *process.Sampler
	every   time.Duration
	mu      sync.Mutex
	tops    map[topChoice]*liveTop
	closed  bool
}

type topChoice struct {
	all, net bool
}

// NewTopHandler serves the groups of the host, with the rules of workDir's
// .telltale/groups.yaml applied over the built-in ones.
func NewTopHandler(workDir string) *TopHandler {
	return &TopHandler{workDir: workDir, sampler: process.NewSampler(), every: groups.DefaultInterval, tops: map[topChoice]*liveTop{}}
}

func (h *TopHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var choice topChoice
	var err error
	if choice.all, err = queryBool(r, "all"); err == nil {
		choice.net, err = queryBool(r, "net")
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	live, err := h.live(choice)
	if errors.Is(err, groups.ErrNetUnsupported) {
		http.Error(w, err.Error(), http.StatusNotImplemented)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var body any
	switch r.URL.Path {
	case TopMount:
		body, err = live.latest(r.Context())
	case TopProcessesPath:
		var filter groups.ProcessFilter
		if filter, err = processFilter(r); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		body, err = live.processes(r.Context(), filter)
	case TopFacetsPath:
		var pid int
		if pid, err = queryPID(r); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		body, err = live.facets(r.Context(), pid)
	default:
		http.NotFound(w, r)
		return
	}
	if errors.Is(err, groups.ErrNotListed) {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		http.Error(w, fmt.Sprintf("encode %s: %v", r.URL.Path, err), http.StatusInternalServerError)
	}
}

// processFilter is the program (name=) or the process (pid=) a processes
// request asks for: one, not both.
func processFilter(r *http.Request) (groups.ProcessFilter, error) {
	name, pid := r.URL.Query().Get("name"), r.URL.Query().Get("pid")
	switch {
	case (name == "") == (pid == ""):
		return groups.ProcessFilter{}, errors.New("give a program's name= or a process's pid=, one of them")
	case name != "":
		return groups.ProcessFilter{Name: name}, nil
	}
	parsed, err := queryPID(r)
	return groups.ProcessFilter{PID: parsed}, err
}

func queryPID(r *http.Request) (int, error) {
	pid := r.URL.Query().Get("pid")
	parsed, err := strconv.Atoi(pid)
	if err != nil || parsed < 1 {
		return 0, fmt.Errorf("pid=%q is not a process id", pid)
	}
	return parsed, nil
}

func queryBool(r *http.Request, name string) (bool, error) {
	value := r.URL.Query().Get(name)
	if value == "" {
		return false, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s=%q is not true or false", name, value)
	}
	return parsed, nil
}

// live is the Top of choice, started on first ask.
func (h *TopHandler) live(choice topChoice) (*liveTop, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, errors.New("the server is stopping")
	}
	if live, ok := h.tops[choice]; ok {
		return live, nil
	}
	catalog, err := groups.CatalogFile(h.workDir)
	if err != nil {
		return nil, err
	}
	options, err := groups.Params{All: choice.all, Net: choice.net, Catalog: catalog, Interval: h.every.String()}.Options()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	top, err := groups.NewTop(ctx, h.sampler, options)
	if err != nil {
		cancel()
		return nil, err
	}
	live := &liveTop{top: top, cancel: cancel, asked: time.Now(), first: make(chan struct{}), done: make(chan struct{})}
	h.tops[choice] = live
	go live.run(ctx, h.every, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.tops[choice] == live {
			delete(h.tops, choice)
		}
	})
	return live, nil
}

// Close stops every live Top.
func (h *TopHandler) Close() error {
	h.mu.Lock()
	h.closed = true
	tops := make([]*liveTop, 0, len(h.tops))
	for _, live := range h.tops {
		tops = append(tops, live)
	}
	h.mu.Unlock()
	for _, live := range tops {
		live.cancel()
		<-live.done
	}
	return nil
}

// liveTop reads a Top every interval until it has not been asked for in
// topIdle, keeping its latest frame or why it failed.
type liveTop struct {
	top    *groups.Top
	cancel context.CancelFunc
	mu     sync.Mutex
	frame  groups.Frame
	err    error
	asked  time.Time
	first  chan struct{}
	done   chan struct{}
}

func (l *liveTop) run(ctx context.Context, every time.Duration, forget func()) {
	defer close(l.done)
	defer l.top.Close()
	defer forget()
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for read := 0; ; read++ {
		frame, err := l.top.Tick(ctx)
		l.mu.Lock()
		if err == nil {
			l.frame = frame
		}
		l.err = err
		idle := time.Since(l.asked) > topIdle
		l.mu.Unlock()
		if read == 0 {
			close(l.first)
		}
		if idle {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// latest is the latest frame, once one was read.
func (l *liveTop) latest(ctx context.Context) (groups.Frame, error) {
	l.mu.Lock()
	l.asked = time.Now()
	l.mu.Unlock()
	select {
	case <-ctx.Done():
		return groups.Frame{}, ctx.Err()
	case <-l.first:
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.frame, l.err
}

// processes are the processes of the latest frame filter picks.
func (l *liveTop) processes(ctx context.Context, filter groups.ProcessFilter) (TopProcesses, error) {
	frame, err := l.latest(ctx)
	if err != nil {
		return TopProcesses{}, err
	}
	picked := l.top.Processes(filter)
	if picked == nil {
		picked = []groups.ProcessSample{}
	}
	return TopProcesses{SampledAt: frame.SampledAt, Tick: frame.Tick, Host: frame.Host, Processes: picked}, nil
}

// facets are the facets of a process the latest frame listed.
func (l *liveTop) facets(ctx context.Context, pid int) (groups.Facets, error) {
	if _, err := l.latest(ctx); err != nil {
		return groups.Facets{}, err
	}
	return l.top.Facets(ctx, pid)
}
