package read

import (
	"errors"
	"fmt"

	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/tracing"
)

// ResolveRuns opens every armed capture of explicitly selected runs.
func ResolveRuns(scope Scope) (_ *Selection, err error) {
	if scope.DataDir == "" || scope.Run == "" && len(scope.Runs) == 0 {
		return nil, fmt.Errorf("replay requires a data dir and at least one run ID")
	}
	if scope.Run != "" && len(scope.Runs) > 0 {
		return nil, fmt.Errorf("--run and --runs are mutually exclusive")
	}
	ids := scope.Runs
	if scope.Run != "" {
		ids = []string{scope.Run}
	}
	selected := &Selection{}
	defer func() {
		if err != nil {
			err = errors.Join(err, selected.Close())
		}
	}()
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return nil, fmt.Errorf("run %q selected twice", id)
		}
		seen[id] = true
		info, err := telltale.Get(telltale.GetOptions{DataDir: scope.DataDir, RunID: id})
		if err != nil {
			return nil, err
		}
		if info.Unavailable != "" {
			return nil, fmt.Errorf("replay %s: %s", id, info.Unavailable)
		}
		selected.Runs = append(selected.Runs, info)
	}
	for _, info := range selected.Runs {
		run, live, err := telltale.Live(scope.DataDir, info.RunID)
		if err != nil {
			return nil, err
		}
		var runtime *tracing.Runtime
		if live {
			runtime = run.Runtime()
		} else {
			var closeStore func() error
			runtime, closeStore, err = telltale.Reopen(scope.DataDir, info.RunID)
			if err != nil {
				return nil, err
			}
			selected.AddCloser(closeStore)
		}
		refs, err := RunRefs(info, runtime)
		if err != nil {
			return nil, err
		}
		selected.Refs = append(selected.Refs, refs...)
	}
	return selected, nil
}
