package traces

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/flanksource/telltale/internal/safefs"
)

// KeepRuns is how many runs' trace stores Prune keeps by default.
const KeepRuns = 20

// Prune removes every run store under workDir/.telltale/traces but the newest
// keep, newest by run id (a run id is its start time). A work dir that has
// never traced a run has nothing to prune.
func Prune(workDir string, keep int) error {
	if keep < 1 {
		return fmt.Errorf("prune trace stores: keep must be at least 1, got %d", keep)
	}
	root := filepath.Join(workDir, Dir)
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("prune trace stores: %w", err)
	}
	var runs []string
	for _, entry := range entries {
		if entry.IsDir() {
			runs = append(runs, entry.Name())
		}
	}
	if len(runs) <= keep {
		return nil
	}
	slices.Sort(runs)
	var errs []error
	for _, run := range runs[:len(runs)-keep] {
		errs = append(errs, safefs.SafeRemove(filepath.Join(root, run), safefs.RemoveContext{AllowedRoot: root}))
	}
	return errors.Join(errs...)
}
