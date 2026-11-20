package groups

import (
	"cmp"
	"context"
	"errors"
	"os"
	"slices"
	"sync"

	"github.com/flanksource/gavel/sdk/status"
)

// gavelRule names the catalog rule whose groups are gavel processes.
const gavelRule = "gavel"

// SlotPool is one gavel slot pool as the gavel processes shown use it.
type SlotPool struct {
	// Name is "global" or "project/<id>".
	Name string `json:"name"`
	// Project is the git common directory of a project pool.
	Project string `json:"project,omitempty"`
	Limit   int    `json:"limit"`
	Held    int    `json:"held"`
	Waiting int    `json:"waiting"`
}

// SlotPools adds up the leases of statuses by pool, global first.
func SlotPools(statuses []status.Status) []SlotPool {
	byName := map[string]*SlotPool{}
	for _, s := range statuses {
		for _, lease := range s.Slots {
			pool, ok := byName[lease.Pool]
			if !ok {
				pool = &SlotPool{Name: lease.Pool}
				byName[lease.Pool] = pool
			}
			pool.Project = cmp.Or(pool.Project, lease.Project)
			pool.Limit = max(pool.Limit, lease.Limit)
			if lease.State == status.LeaseWaiting {
				pool.Waiting++
			} else {
				pool.Held++
			}
		}
	}
	pools := make([]SlotPool, 0, len(byName))
	for _, pool := range byName {
		pools = append(pools, *pool)
	}
	rank := func(p SlotPool) int {
		if p.Name == "global" {
			return 0
		}
		return 1
	}
	slices.SortFunc(pools, func(a, b SlotPool) int {
		return cmp.Or(cmp.Compare(rank(a), rank(b)), cmp.Compare(a.Project, b.Project), cmp.Compare(a.Name, b.Name))
	})
	return pools
}

// gavelStatuses follows what each gavel process shown publishes, by pid,
// until it is closed.
type gavelStatuses struct {
	cache  string
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	byPID  map[int]*gavelWatch
}

type gavelWatch struct {
	cancel context.CancelFunc
	latest *status.Status
	err    error
}

func newGavelStatuses(cache string) *gavelStatuses {
	ctx, cancel := context.WithCancel(context.Background())
	return &gavelStatuses{cache: cache, ctx: ctx, cancel: cancel, byPID: map[int]*gavelWatch{}}
}

// GavelReading is what a gavel process published, or why it could not be read.
type GavelReading struct {
	Status *status.Status
	Err    error
}

// of follows the processes pids, stops following the others, and returns
// what each has published so far. A process that has published nothing yet
// is retried on the next call.
func (g *gavelStatuses) of(pids []int) map[int]GavelReading {
	g.mu.Lock()
	defer g.mu.Unlock()
	wanted := map[int]bool{}
	for _, pid := range pids {
		wanted[pid] = true
		if _, ok := g.byPID[pid]; !ok {
			g.follow(pid)
		}
	}
	readings := map[int]GavelReading{}
	for pid, watch := range g.byPID {
		if !wanted[pid] {
			watch.cancel()
			delete(g.byPID, pid)
			continue
		}
		readings[pid] = GavelReading{Status: watch.latest, Err: watch.err}
	}
	return readings
}

// follow starts watching pid, once it has published its status.
func (g *gavelStatuses) follow(pid int) {
	if _, err := os.Stat(status.Path(g.cache, pid)); errors.Is(err, os.ErrNotExist) {
		return
	}
	ctx, cancel := context.WithCancel(g.ctx)
	watch := &gavelWatch{cancel: cancel}
	g.byPID[pid] = watch
	watcher, err := status.Watch(ctx, status.WatchOptions{Cache: g.cache, PID: pid})
	if err != nil {
		watch.err = err
		return
	}
	go func() {
		for update := range watcher.Updates() {
			g.mu.Lock()
			watch.latest = &update
			g.mu.Unlock()
		}
		g.mu.Lock()
		defer g.mu.Unlock()
		watch.err = watcher.Err()
		// A process whose status went away has finished; it is followed
		// again should it publish once more.
		if g.byPID[pid] == watch && watch.err == nil {
			delete(g.byPID, pid)
		}
	}()
}

// close stops following every process.
func (g *gavelStatuses) close() {
	g.cancel()
	g.mu.Lock()
	defer g.mu.Unlock()
	clear(g.byPID)
}
