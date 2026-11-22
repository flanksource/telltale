package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/flanksource/telltale/pkg/tracing/groups"
	"github.com/flanksource/telltale/pkg/tracing/process"
)

// TopMount serves the latest frame of the host's process groups, read live
// and kept nowhere: GET with all=true to include the processes no tool's
// group holds, net=true to read network bytes with nettop. Under it,
// TopProcessesPath serves the processes running a program (name=), a
// process and those under it (pid=), or those whose name, command or pid
// holds a search text (q=).
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
	Polling   TopPolling             `json:"polling"`
}

// TopFrame is the latest live frame with the server's recommended polling interval.
type TopFrame struct {
	groups.Frame
	Polling TopPolling `json:"polling"`
}

// TopHandler samples on demand. All views share one rolling CPU budget.
type TopHandler struct {
	sampler  *process.Sampler
	gate     chan struct{}
	done     chan struct{}
	tops     map[topChoice]*liveTop
	budget   topBudget
	lastCost time.Duration
}

type topChoice struct {
	all, net bool
}

// NewTopHandler serves the groups of the host, with the rules of the
// groups.yaml its config dirs keep applied over the built-in ones.
func NewTopHandler() *TopHandler {
	return &TopHandler{sampler: process.NewSampler(), gate: make(chan struct{}, 1), done: make(chan struct{}), tops: map[topChoice]*liveTop{}}
}

func (h *TopHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == TopActionPath {
		processAction(w, r)
		return
	}
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
	var read topRead
	switch r.URL.Path {
	case TopMount:
		read = func(live *liveTop, polling TopPolling) (any, error) {
			frame := live.frame
			overhead := *frame.Overhead
			percent := h.budget.cpuPercent()
			overhead.CPUPercent = &percent
			frame.Overhead = &overhead
			return TopFrame{Frame: frame, Polling: polling}, nil
		}
	case TopProcessesPath:
		var filter groups.ProcessFilter
		if filter, err = processFilter(r); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		read = func(live *liveTop, polling TopPolling) (any, error) {
			picked := live.top.Processes(filter)
			if picked == nil {
				picked = []groups.ProcessSample{}
			}
			return TopProcesses{SampledAt: live.frame.SampledAt, Tick: live.frame.Tick, Host: live.frame.Host, Processes: picked, Polling: polling}, nil
		}
	case TopFacetsPath:
		var pid int
		if pid, err = queryPID(r); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		read = func(live *liveTop, _ TopPolling) (any, error) { return live.top.Facets(r.Context(), pid) }
	default:
		http.NotFound(w, r)
		return
	}
	body, err := h.latest(r.Context(), choice, read)
	if errors.Is(err, groups.ErrNetUnsupported) {
		http.Error(w, err.Error(), http.StatusNotImplemented)
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

// processFilter is the program (name=), the process (pid=) or the search
// text (q=) a processes request asks for: exactly one of them.
func processFilter(r *http.Request) (groups.ProcessFilter, error) {
	name, pid, query := r.URL.Query().Get("name"), r.URL.Query().Get("pid"), r.URL.Query().Get("q")
	given := 0
	for _, value := range []string{name, pid, query} {
		if value != "" {
			given++
		}
	}
	switch {
	case given != 1:
		return groups.ProcessFilter{}, errors.New("give a program's name=, a process's pid= or a search q=, one of them")
	case name != "":
		return groups.ProcessFilter{Name: name}, nil
	case query != "":
		return groups.ProcessFilter{Query: query}, nil
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

func (h *TopHandler) open(ctx context.Context, choice topChoice) (*liveTop, error) {
	catalog, err := groups.CatalogFile()
	if err != nil {
		return nil, err
	}
	options, err := groups.Options{All: choice.all, Net: choice.net, Catalog: catalog}.TopOptions()
	if err != nil {
		return nil, err
	}
	options.OnDemand = true
	top, err := groups.NewTop(ctx, h.sampler, options)
	if err != nil {
		return nil, err
	}
	live := &liveTop{top: top}
	h.tops[choice] = live
	return live, nil
}

// Close stops every live Top.
func (h *TopHandler) Close() error {
	h.gate <- struct{}{}
	defer func() { <-h.gate }()
	select {
	case <-h.done:
		return nil
	default:
		close(h.done)
	}
	for _, live := range h.tops {
		live.top.Close()
	}
	return nil
}

type liveTop struct {
	top     *groups.Top
	frame   groups.Frame
	err     error
	started time.Time
	cost    time.Duration
}

type topRead func(*liveTop, TopPolling) (any, error)

var errTopClosed = errors.New("the server is stopping")

func (h *TopHandler) latest(ctx context.Context, choice topChoice, read topRead) (any, error) {
	for {
		body, wait, err := h.read(ctx, choice, read)
		if wait == 0 || err != nil {
			return body, err
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-h.done:
			timer.Stop()
			return nil, errTopClosed
		case <-timer.C:
		}
	}
}

func (h *TopHandler) read(ctx context.Context, choice topChoice, read topRead) (any, time.Duration, error) {
	select {
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	case h.gate <- struct{}{}:
	}
	defer func() { <-h.gate }()
	select {
	case <-h.done:
		return nil, 0, errTopClosed
	default:
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	now := time.Now()
	live := h.tops[choice]
	estimate := h.lastCost
	if live != nil {
		estimate = live.cost
	}
	wait := h.budget.wait(now, estimate)
	if live != nil {
		wait = max(wait, live.started.Add(topPollInterval(live.cost)).Sub(now))
	}
	if wait == 0 {
		var err error
		if live, err = h.collect(ctx, choice); err != nil {
			return nil, 0, err
		}
	} else if live == nil || (live.frame.Tick == 0 && live.err == nil) {
		return nil, wait, nil
	}
	if live.err != nil {
		return nil, 0, live.err
	}
	floor := max(topPollInterval(live.cost), h.budget.wait(time.Now(), live.cost))
	body, err := read(live, TopPolling{MinimumIntervalMS: (floor + time.Millisecond - 1).Milliseconds()})
	return body, 0, err
}

func (h *TopHandler) collect(ctx context.Context, choice topChoice) (*liveTop, error) {
	started := time.Now()
	before, err := groups.CPUTime()
	if err != nil {
		return nil, err
	}
	live := h.tops[choice]
	if live == nil {
		if live, err = h.open(ctx, choice); err != nil {
			return nil, err
		}
	}
	frame, tickErr := live.top.Tick(ctx)
	after, err := groups.CPUTime()
	if err != nil {
		return nil, errors.Join(tickErr, err)
	}
	cost := after - before
	h.budget.record(time.Now(), cost)
	h.lastCost = cost
	live.started, live.cost = started, cost
	if err := ctx.Err(); err != nil && tickErr != nil {
		return nil, err
	}
	live.err = tickErr
	if tickErr == nil {
		live.frame = frame
	}
	return live, nil
}
