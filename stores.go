package telltale

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/tracing"
)

// StandingStore is a store something writes continuously rather than a run,
// such as the OTLP receiver's: List, Get and Reopen serve it as a pseudo-run
// under its reserved ID.
type StandingStore struct {
	// ID is the run id it is listed and read under; it is never run-shaped.
	ID    string
	Title string
	// Info describes the store in dataDir, false when dataDir has none. It
	// sets Status (running while something writes it, else standing), Dir
	// and Captures; ID and Standing are set for it.
	Info func(dataDir string) (RunInfo, bool, error)
	// Open opens the store in dataDir to read, with the close that stops
	// using it.
	Open func(dataDir string) (*tracing.Runtime, func() error, error)
}

var standing = struct {
	mu     sync.RWMutex
	stores map[string]StandingStore
}{stores: map[string]StandingStore{}}

// MustRegisterStore registers a standing store from an init func, panicking
// on an ID a run could take or another store holds.
func MustRegisterStore(store StandingStore) {
	if store.ID == "" || strings.ContainsAny(store.ID, `/\`) || strings.HasPrefix(store.ID, traces.RunPrefix) {
		panic(fmt.Errorf("register standing store %q: its id must be a plain name that is no run id (%s…)", store.ID, traces.RunPrefix))
	}
	if store.Info == nil || store.Open == nil {
		panic(fmt.Errorf("register standing store %q: Info and Open are required", store.ID))
	}
	standing.mu.Lock()
	defer standing.mu.Unlock()
	if _, ok := standing.stores[store.ID]; ok {
		panic(fmt.Errorf("register standing store %q: it is registered twice", store.ID))
	}
	standing.stores[store.ID] = store
}

func standingStore(id string) (StandingStore, bool) {
	standing.mu.RLock()
	defer standing.mu.RUnlock()
	store, ok := standing.stores[id]
	return store, ok
}

// standingInfos describe the registered stores dataDir has, by ID.
func standingInfos(dataDir string) ([]RunInfo, error) {
	standing.mu.RLock()
	ids := make([]string, 0, len(standing.stores))
	for id := range standing.stores {
		ids = append(ids, id)
	}
	standing.mu.RUnlock()
	slices.Sort(ids)
	infos := []RunInfo{}
	for _, id := range ids {
		info, err := getStanding(dataDir, id)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		infos = append(infos, info)
	}
	return infos, nil
}

// getStanding describes the standing store id of dataDir; one dataDir does not
// have is os.ErrNotExist.
func getStanding(dataDir, id string) (RunInfo, error) {
	store, _ := standingStore(id)
	info, ok, err := store.Info(dataDir)
	if err != nil {
		return RunInfo{}, fmt.Errorf("get standing store %s: %w", id, err)
	}
	if !ok {
		return RunInfo{}, fmt.Errorf("get standing store %s: %w", id, os.ErrNotExist)
	}
	if info.Status != StatusRunning && info.Status != StatusStanding {
		return RunInfo{}, fmt.Errorf("standing store %s reported status %q, want running or standing", id, info.Status)
	}
	info.RunID, info.Standing = id, true
	return info, nil
}

// Reopen opens the store of runID in dataDir to read: a standing store
// through its own opener, a run's from its directory.
func Reopen(dataDir, runID string) (*tracing.Runtime, func() error, error) {
	if store, ok := standingStore(runID); ok {
		return store.Open(dataDir)
	}
	return traces.Reopen(traces.StoreDir(dataDir, runID))
}
