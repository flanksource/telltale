// Package traceserve opens a trace results store and serves the routes a trace
// viewer reads it through, as one handler a host mounts under a path of its
// own. It serves no UI: the host renders the store with clicky-ui.
package traceserve

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/flanksource/commons-db/query"

	// A store's result profiles page through the sqlite provider.
	_ "github.com/flanksource/commons-db/query/providers/sqlite"
	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/commons-db/recordstore/probe"
	"github.com/flanksource/commons-db/recordstore/recordresults"
	"github.com/flanksource/telltale/pkg/tracing"
)

const (
	// Prefix names a store's result profiles, traces/<kind>, and its index
	// connection, connection://traces/traces.
	Prefix = "traces"
	// APIPrefix is where Handler serves its routes, under the path the host
	// mounts it at.
	APIPrefix = "/api/v1"
)

// OpenOptions say which store Open opens and what it serves.
type OpenOptions struct {
	// Dir is the store's directory; Open creates it when it does not exist.
	Dir string
	// Kinds declares the result types the store's streams hold.
	Kinds *tracing.Kinds
	// Retention is how long a stream is kept; an expired one is swept away.
	Retention time.Duration
}

// Open opens the trace results store in options.Dir and returns the runtime
// that serves it and starts captures into it, with a session registry of its
// own a host may replace, and the close that stops using the store.
func Open(options OpenOptions) (*tracing.Runtime, func() error, error) {
	if options.Kinds == nil {
		return nil, nil, errors.New("open trace store: the kinds it holds are required")
	}
	results, err := recordresults.Open(recordresults.OpenOptions{
		Prefix: Prefix, ConnectionName: Prefix, Register: options.Kinds.RegisterResultTypes,
		Settings: recordstore.Settings{Prefix: Prefix, Backend: recordstore.BackendSQLite, Dir: options.Dir, TTL: options.Retention},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("open trace store %s: %w", options.Dir, err)
	}
	return &tracing.Runtime{
		Kinds: options.Kinds, Results: results,
		Probes:   probe.NewManager(probe.ManagerOptions{LockDir: filepath.Join(options.Dir, "locks")}),
		Sessions: query.NewSessionRegistry(query.RegistryOptions{BeforeRead: results.Registry.BeforeRead}),
	}, results.Close, nil
}

// ReopenOptions say which store Reopen serves.
type ReopenOptions struct {
	// Dir is the directory a closed store was left in, such as a finished
	// run's; Reopen never creates it.
	Dir string
	// Kinds declares the result types the store's streams hold.
	Kinds *tracing.Kinds
}

// reopenedRetention is the stream TTL a reopened store is opened with. A
// stream's expiry is set when it is first appended to, and nothing appends to
// a reopened store: it serves no capture, so the TTL never applies.
const reopenedRetention = time.Hour

// Reopen serves the store a closed host left in options.Dir, with the close
// that stops using it. It runs no capture, so a checkpoint is a conflict. A
// store that is gone is os.ErrNotExist; one that is there but does not open is
// an error.
func Reopen(options ReopenOptions) (http.Handler, func() error, error) {
	info, err := os.Stat(options.Dir)
	if err != nil {
		return nil, nil, fmt.Errorf("reopen trace store: %w", err)
	}
	if !info.IsDir() {
		return nil, nil, fmt.Errorf("reopen trace store: %s is not a directory", options.Dir)
	}
	runtime, closeStore, err := Open(OpenOptions{Dir: options.Dir, Kinds: options.Kinds, Retention: reopenedRetention})
	if err != nil {
		return nil, nil, err
	}
	handler, err := Handler(runtime)
	if err != nil {
		return nil, nil, errors.Join(err, closeStore())
	}
	return handler, closeStore, nil
}
