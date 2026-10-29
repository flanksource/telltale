package otelreceiver

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/commons-db/recordstore/owner"
	"github.com/flanksource/commons-db/recordstore/recordresults"
	"github.com/flanksource/commons-db/recordstore/sqlite"
	"github.com/flanksource/telltale/pkg/tracing"
)

type StoreOptions struct {
	WorkDir   string
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
const kind = "otlp"

type Store struct {
	results   *recordresults.Results
	runtime   *tracing.Runtime
	options   StoreOptions
	release   func() error
	lifecycle sync.RWMutex
	write     sync.Mutex
	closed    bool
}

func StorePath(workDir string) string {
	return filepath.Join(workDir, ".telltale", "otel", "records.sqlite")
}

func StoreExists(workDir string) (bool, error) {
	_, err := os.Stat(sqlite.VersionedPath(StorePath(workDir)))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func OpenStore(options StoreOptions) (_ *Store, err error) {
	if options.WorkDir == "" || options.Retention <= 0 {
		return nil, fmt.Errorf("OTLP store requires a work directory and positive retention")
	}
	store := &Store{options: options}
	defer func() {
		if err != nil {
			err = errors.Join(err, store.Close())
		}
	}()
	dir := filepath.Dir(StorePath(options.WorkDir))
	if options.Writable {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, err
		}
		store.release, err = owner.Exclusive(filepath.Join(dir, "receiver.lock"), "telltale OTLP receiver")
		if err != nil {
			return nil, err
		}
	} else if _, err := os.Stat(sqlite.VersionedPath(StorePath(options.WorkDir))); err != nil {
		return nil, err
	}
	store.results, err = recordresults.Open(recordresults.OpenOptions{
		Prefix: "traces", ConnectionName: "receiver",
		Settings: recordstore.Settings{Prefix: "receiver", Backend: recordstore.BackendSQLite, Dir: dir, TTL: options.Retention},
		Register: func(registry *recordresults.Registry) error {
			return recordresults.RegisterResultType(registry, recordresults.ResultType[Span]{
				Kind: kind, Title: "Received OTLP spans", TimeColumn: "startedAt", KeyColumn: "key",
				Retention: recordstore.RetainRows, Compressed: []string{"native"},
				Indexes: []recordstore.IndexDef{{Columns: []string{"traceId"}}, {Columns: []string{"service", "startedAt"}}},
			})
		},
	})
	if err != nil {
		return nil, err
	}
	store.runtime = &tracing.Runtime{Results: store.results}
	return store, nil
}

func (s *Store) Close() error {
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	var err error
	if s.results != nil {
		err = s.results.Close()
	}
	if s.release != nil {
		err = errors.Join(err, s.release())
	}
	return err
}
