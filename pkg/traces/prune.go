package traces

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/flanksource/telltale/internal/safefs"
)

// KeepRuns is how many runs' trace stores Prune keeps by default.
const KeepRuns = 20

// Prune removes every run store under dataDir/traces but the newest
// keep, newest by recorded start time. A store without a manifest uses its
// directory modification time. A data dir that has
// never traced a run has nothing to prune.
func Prune(dataDir string, keep int) error {
	if keep < 1 {
		return fmt.Errorf("prune trace stores: keep must be at least 1, got %d", keep)
	}
	root := filepath.Join(dataDir, Dir)
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("prune trace stores: %w", err)
	}
	type storedRun struct {
		name    string
		started time.Time
	}
	var runs []storedRun
	for _, entry := range entries {
		if entry.IsDir() {
			started, err := storeStartedAt(filepath.Join(root, entry.Name()), entry)
			if err != nil {
				return err
			}
			runs = append(runs, storedRun{entry.Name(), started})
		}
	}
	if len(runs) <= keep {
		return nil
	}
	slices.SortFunc(runs, func(a, b storedRun) int {
		if compared := a.started.Compare(b.started); compared != 0 {
			return compared
		}
		return cmp.Compare(a.name, b.name)
	})
	var errs []error
	for _, run := range runs[:len(runs)-keep] {
		errs = append(errs, safefs.SafeRemove(filepath.Join(root, run.name), safefs.RemoveContext{AllowedRoot: root}))
	}
	return errors.Join(errs...)
}

func storeStartedAt(dir string, entry os.DirEntry) (time.Time, error) {
	data, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if errors.Is(err, os.ErrNotExist) {
		info, err := entry.Info()
		if err != nil {
			return time.Time{}, err
		}
		return info.ModTime(), nil
	}
	if err != nil {
		return time.Time{}, err
	}
	var manifest struct {
		StartedAt time.Time `json:"startedAt"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return time.Time{}, fmt.Errorf("prune trace store %s: %w", dir, err)
	}
	if manifest.StartedAt.IsZero() {
		return time.Time{}, fmt.Errorf("prune trace store %s: manifest has no start time", dir)
	}
	return manifest.StartedAt, nil
}
