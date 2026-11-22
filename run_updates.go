package telltale

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/flanksource/clicky/entity"
	"github.com/flanksource/clicky/sse"
	"github.com/flanksource/telltale/internal/livestream"
	"github.com/flanksource/telltale/pkg/traces"
	"github.com/fsnotify/fsnotify"
)

var runChanged sse.Notifier

// ServeRunUpdates streams the current list and its changes from local and peer runs.
func ServeRunUpdates(w http.ResponseWriter, r *http.Request, dataDir string, allowed []string, read func() (RunsList, error)) {
	livestream.Serve(w, r, func(ctx context.Context, send entity.StreamSend) error {
		root, err := filepath.Abs(filepath.Join(dataDir, traces.Dir))
		if err != nil {
			return err
		}
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			return err
		}
		defer watcher.Close()
		wake, unsubscribe := runChanged.Subscribe()
		defer unsubscribe()
		var previous []byte
		for {
			if err := watchRunDirs(watcher, root, allowed); err != nil {
				return err
			}
			runs, err := read()
			if err != nil {
				return err
			}
			payload, err := json.Marshal(runs)
			if err != nil {
				return err
			}
			if !slices.Equal(payload, previous) {
				if err := send(entity.StreamEvent{Data: payload}); err != nil {
					return err
				}
				previous = payload
			}
			if len(allowed) > 0 && !slices.ContainsFunc(runs.Runs, func(run MountedRun) bool { return run.Status == StatusRunning || run.Standing }) {
				return nil
			}
			if err := waitRunChange(ctx, watcher, root, wake); err != nil {
				return err
			}
		}
	})
}

func watchRunDirs(watcher *fsnotify.Watcher, root string, allowed []string) error {
	err := watcher.Add(root)
	if errors.Is(err, os.ErrNotExist) {
		for parent := filepath.Dir(root); ; parent = filepath.Dir(parent) {
			if err = watcher.Add(parent); !errors.Is(err, os.ErrNotExist) || parent == filepath.Dir(parent) {
				break
			}
		}
		if err != nil {
			return err
		}
		if err = watcher.Add(root); errors.Is(err, os.ErrNotExist) {
			return nil
		}
	}
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() && (len(allowed) == 0 || slices.Contains(allowed, entry.Name())) {
			if err := watcher.Add(filepath.Join(root, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

func waitRunChange(ctx context.Context, watcher *fsnotify.Watcher, root string, wake <-chan struct{}) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wake:
			return nil
		case err := <-watcher.Errors:
			if errors.Is(err, fsnotify.ErrEventOverflow) {
				return nil
			}
			return err
		case event := <-watcher.Events:
			if strings.HasPrefix(root+string(filepath.Separator), event.Name+string(filepath.Separator)) ||
				filepath.Dir(event.Name) == root || filepath.Base(event.Name) == ManifestFile {
				return nil
			}
		}
	}
}
