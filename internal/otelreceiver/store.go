package otelreceiver

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/commons-db/recordstore/owner"
	"github.com/flanksource/commons-db/recordstore/recordresults"
	"github.com/flanksource/commons-db/recordstore/sqlite"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/spans"
)

type StoreOptions struct {
	DataDir   string
	Retention time.Duration
	Writable  bool
}

type ReadOptions struct {
	From, Until time.Time
	TraceIDs    []string
	Keys        []string
	Limit       int
}

type Outcome struct {
	Accepted, Rejected, Duplicates int64
	Reason                         string
}

const stream = "received"

type Store struct {
	results   *recordresults.Results
	runtime   *tracing.Runtime
	options   StoreOptions
	release   func() error
	lifecycle sync.RWMutex
	write     sync.Mutex
	closed    bool
	// served is the data dir key this store is registered as serving under.
	served string
}

// StorePath is the receiver's spans store under the data dir.
func StorePath(dataDir string) string {
	return filepath.Join(dataDir, "otel", "spans", "records.sqlite")
}

// lockPath is the lock the writing receiver holds, beside the stores of every
// version.
func lockPath(dataDir string) string {
	return filepath.Join(dataDir, "otel", "receiver.lock")
}

func StoreExists(dataDir string) (bool, error) {
	_, err := os.Stat(sqlite.VersionedPath(StorePath(dataDir)))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func OpenStore(options StoreOptions) (_ *Store, err error) {
	if options.DataDir == "" || options.Retention <= 0 {
		return nil, fmt.Errorf("OTLP store requires a data directory and positive retention")
	}
	store := &Store{options: options}
	defer func() {
		if err != nil {
			err = errors.Join(err, store.Close())
		}
	}()
	dir := filepath.Dir(StorePath(options.DataDir))
	if options.Writable {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, err
		}
		store.release, err = owner.Exclusive(lockPath(options.DataDir), "telltale OTLP receiver")
		if err != nil {
			return nil, err
		}
	} else if _, err := os.Stat(sqlite.VersionedPath(StorePath(options.DataDir))); err != nil {
		return nil, err
	}
	store.results, err = recordresults.Open(recordresults.OpenOptions{
		Prefix: "traces", ConnectionName: "receiver",
		Settings: recordstore.Settings{Prefix: "receiver", Backend: recordstore.BackendSQLite, Dir: dir, TTL: options.Retention},
		Register: func(registry *recordresults.Registry) error {
			resultType := spans.ResultType()
			resultType.Retention = recordstore.RetainRows
			return recordresults.RegisterResultType(registry, resultType)
		},
	})
	if err != nil {
		return nil, err
	}
	kinds := tracing.NewKinds()
	if err := kinds.RegisterKind(spans.Kind, spans.NewKind(nil)); err != nil {
		return nil, err
	}
	store.runtime = &tracing.Runtime{
		Results: store.results, Kinds: kinds,
		Sessions: query.NewSessionRegistry(query.RegistryOptions{BeforeRead: store.results.Registry.BeforeRead}),
	}
	if options.Writable {
		if err := store.serve(); err != nil {
			return nil, err
		}
	}
	return store, nil
}

// Runtime is the store's runtime, serving its spans kind.
func (s *Store) Runtime() *tracing.Runtime { return s.runtime }

// serve registers the writable store as this process's receiver for its work
// dir, so the receiver pseudo-run reads it rather than reopening the file.
func (s *Store) serve() error {
	key, err := servingKey(s.options.DataDir)
	if err != nil {
		return err
	}
	serving.mu.Lock()
	defer serving.mu.Unlock()
	serving.stores[key], s.served = s, key
	return nil
}

func (s *Store) unserve() {
	serving.mu.Lock()
	defer serving.mu.Unlock()
	if s.served != "" && serving.stores[s.served] == s {
		delete(serving.stores, s.served)
	}
}

func (s *Store) Close() error {
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.unserve()
	var err error
	if s.results != nil {
		err = s.results.Close()
	}
	if s.release != nil {
		err = errors.Join(err, s.release())
	}
	return err
}
