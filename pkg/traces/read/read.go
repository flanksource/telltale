// Package read resolves recorded runs and reads their streams through result profiles.
package read

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/tracing"
)

type Scope struct {
	DataDir string
	Run     string
	Runs    []string
	Since   time.Time
	Result  string
}

type Ref struct {
	RunID   string
	Runtime *tracing.Runtime
	tracing.TraceRef
}

type Selection struct {
	Runs    []telltale.RunInfo
	Refs    []Ref
	closers []func() error
}

// AddCloser transfers the lifetime of a constituent selection to this one.
func (s *Selection) AddCloser(close func() error) { s.closers = append(s.closers, close) }

func (s *Selection) Close() error {
	var errs []error
	for _, closeStore := range s.closers {
		errs = append(errs, closeStore())
	}
	return errors.Join(errs...)
}

type Query struct {
	From, Until     time.Time
	AfterSeq, ToSeq int64
	Filters         []string
	Search, Where   string
	Limit           int
	Cursor          string
	All             bool
	NextCursor      func(string) `json:"-"`
	seqOrder        bool
}

func Resolve(kind string, scope Scope) (_ *Selection, err error) {
	kinds, err := traces.Kinds()
	if err != nil {
		return nil, err
	}
	if _, err := traces.Lookup(kinds, kind); err != nil {
		return nil, err
	}
	if scope.Result != "" {
		storage := kind + "_" + scope.Result
		owner, result := kinds.ResultOf(storage)
		if owner != kind || result == "" {
			return nil, fmt.Errorf("kind %s has no result %q", kind, scope.Result)
		}
		kind = storage
	}
	if scope.Run != "" && len(scope.Runs) > 0 {
		return nil, fmt.Errorf("--run and --runs are mutually exclusive")
	}
	if scope.DataDir == "" {
		return nil, fmt.Errorf("read %s: a data dir is required", kind)
	}
	ids := scope.Runs
	if scope.Run != "" {
		ids = []string{scope.Run}
	}
	var runs []telltale.RunInfo
	if len(ids) == 0 {
		limit := 1
		if !scope.Since.IsZero() {
			limit = 0
		}
		owner, _ := kinds.ResultOf(kind)
		runs, err = telltale.List(telltale.ListOptions{DataDir: scope.DataDir, Kind: owner, Since: scope.Since, Limit: limit})
		if err != nil {
			return nil, err
		}
	} else {
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
			if !scope.Since.IsZero() && !info.Standing && info.StartedAt.Before(scope.Since) {
				continue
			}
			runs = append(runs, info)
		}
	}
	if len(runs) == 0 {
		return nil, fmt.Errorf("no run containing %s matches the selection", kind)
	}
	selected := &Selection{}
	defer func() {
		if err != nil {
			err = errors.Join(err, selected.Close())
		}
	}()
	for _, run := range runs {
		if run.Unavailable != "" {
			return nil, fmt.Errorf("read %s: %s", run.RunID, run.Unavailable)
		}
		runtime, closeStore, err := telltale.Reopen(scope.DataDir, run.RunID)
		if err != nil {
			return nil, err
		}
		selected.closers = append(selected.closers, closeStore)
		refs, err := RunRefs(run, runtime)
		if err != nil {
			return nil, err
		}
		found := false
		for _, ref := range refs {
			if ref.Kind == kind {
				selected.Refs = append(selected.Refs, ref)
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("run %s has no %s capture", run.RunID, kind)
		}
	}
	return selected, nil
}

func (q Query) Validate() error {
	if q.Limit < 0 {
		return fmt.Errorf("limit must be nonnegative")
	}
	if q.AfterSeq < 0 || q.ToSeq < 0 || q.ToSeq > 0 && q.ToSeq < q.AfterSeq {
		return fmt.Errorf("invalid seq window %d..%d", q.AfterSeq, q.ToSeq)
	}
	if !q.From.IsZero() && !q.Until.IsZero() && !q.From.Before(q.Until) {
		return fmt.Errorf("from must precede until")
	}
	for _, filter := range q.Filters {
		col, expr, ok := strings.Cut(filter, "=")
		if !ok || strings.TrimSpace(col) == "" || strings.TrimSpace(expr) == "" {
			return fmt.Errorf("filter %q must be col=expr", filter)
		}
	}
	_, err := q.predicate(context.Background())
	return err
}
