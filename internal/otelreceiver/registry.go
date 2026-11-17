package otelreceiver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/commons-db/recordstore/owner"
	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/spans"
)

// The receiver's store is the standing store SourceID: `telltale trace list`,
// `telltale records spans --run receiver` and /api/traces/receiver read it as
// a pseudo-run.
func init() {
	telltale.MustRegisterStore(telltale.StandingStore{ID: SourceID, Title: "OTLP receiver", Info: standingInfo, Open: openStanding})
}

// serving is the writable store this process holds, by absolute data dir, so
// reading it goes through the store that writes it.
var serving = struct {
	mu     sync.Mutex
	stores map[string]*Store
}{stores: map[string]*Store{}}

func servingKey(dataDir string) (string, error) {
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		return "", fmt.Errorf("resolve OTLP receiver data dir %s: %w", dataDir, err)
	}
	return abs, nil
}

func servingStore(dataDir string) (*Store, error) {
	key, err := servingKey(dataDir)
	if err != nil {
		return nil, err
	}
	serving.mu.Lock()
	defer serving.mu.Unlock()
	return serving.stores[key], nil
}

func standingInfo(dataDir string) (telltale.RunInfo, bool, error) {
	exists, err := StoreExists(dataDir)
	if err != nil || !exists {
		return telltale.RunInfo{}, false, err
	}
	status := telltale.StatusStanding
	running, err := receiving(dataDir)
	if err != nil {
		return telltale.RunInfo{}, false, err
	}
	if running {
		status = telltale.StatusRunning
	}
	captures, err := standingCaptures(dataDir)
	if err != nil {
		return telltale.RunInfo{}, false, err
	}
	return telltale.RunInfo{
		Dir: filepath.Dir(lockPath(dataDir)), Status: status,
		Captures: captures,
	}, true, nil
}

func standingCaptures(dataDir string) ([]telltale.CaptureResult, error) {
	runtime, closeStore, err := openStanding(dataDir)
	if err != nil {
		return nil, err
	}
	var captures []telltale.CaptureResult
	_, err = runtime.Results.Backend.Meta(context.Background(), stream)
	if err != nil && !errors.Is(err, recordstore.ErrNotFound) {
		return nil, errors.Join(err, closeStore())
	}
	if err == nil {
		captures = append(captures, telltale.CaptureResult{Kind: spans.Kind, Ref: tracing.TraceRef{Kind: spans.Kind, Stream: stream}})
	}
	for _, kind := range []string{"http_top", "sql_top", "jms_top"} {
		meta, err := runtime.Results.Backend.Meta(context.Background(), profileStream(kind)+"-frames")
		if errors.Is(err, recordstore.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, errors.Join(err, closeStore())
		}
		if meta.HighSeq > 0 {
			storageKind := profileStorageKind(kind)
			captures = append(captures, telltale.CaptureResult{Kind: storageKind, Ref: tracing.TraceRef{Kind: storageKind, Stream: profileStream(kind)}})
		}
	}
	return captures, closeStore()
}

// receiving reports a receiver holding dataDir's store: this process, or a
// live process on this host that published itself as the lock's owner.
func receiving(dataDir string) (bool, error) {
	if store, err := servingStore(dataDir); err != nil || store != nil {
		return store != nil, err
	}
	state, found, err := owner.ReadState(lockPath(dataDir))
	if err != nil || !found {
		return false, err
	}
	host, err := os.Hostname()
	if err != nil {
		return false, fmt.Errorf("OTLP receiver owner probe: %w", err)
	}
	if state.Host != "" && state.Host != host {
		return false, nil
	}
	err = syscall.Kill(state.PID, 0)
	return err == nil || errors.Is(err, syscall.EPERM), nil
}

func openStanding(dataDir string) (*tracing.Runtime, func() error, error) {
	store, err := servingStore(dataDir)
	if err != nil {
		return nil, nil, err
	}
	if store != nil {
		return store.Runtime(), func() error { return nil }, nil
	}
	store, err = OpenStore(StoreOptions{DataDir: dataDir, Retention: DefaultOptions().Retention})
	if err != nil {
		return nil, nil, err
	}
	return store.Runtime(), store.Close, nil
}
