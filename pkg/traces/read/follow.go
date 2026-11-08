package read

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/traces"
)

var errFollowLimit = errors.New("follow limit reached")

func Follow(ctx context.Context, ref Ref, q Query, fn func(query.Row) error) error {
	if err := q.Validate(); err != nil {
		return err
	}
	if q.Cursor != "" {
		return fmt.Errorf("--follow cannot use a page cursor; use --after-seq")
	}
	_, p, err := ref.profile(ctx, "")
	if err != nil {
		return err
	}
	if _, err := q.params(ref, p); err != nil {
		return err
	}
	through := ref.ToSeq
	if q.ToSeq > 0 && (through == 0 || q.ToSeq < through) {
		through = q.ToSeq
	}
	if through > 0 && max(ref.AfterSeq, q.AfterSeq) >= through {
		return nil
	}
	count, last := 0, max(ref.AfterSeq, q.AfterSeq)
	// Finish each native profile read before rendering; output holds no SQLite reader.
	deliver := func(high int64) error {
		window := q
		window.AfterSeq, window.ToSeq = last, high
		window.All, window.seqOrder = false, true
		window.Limit = 500
		window.Cursor = ""
		window.NextCursor = nil
		rows, err := query.CollectRows(Rows(ctx, []Ref{ref}, window))
		if err != nil {
			return err
		}
		for _, row := range rows {
			if err := fn(row); err != nil {
				return err
			}
			last = row["seq"].(int64)
			count++
			if q.Limit > 0 && count >= q.Limit {
				return errFollowLimit
			}
		}
		if len(rows) < window.Limit {
			last = high
		}
		return nil
	}
	backend := ref.Runtime.Results.Backend
	meta, err := backend.Meta(ctx, ref.Stream)
	if err != nil {
		return err
	}
	for err == nil {
		high := meta.HighSeq
		if through > 0 {
			high = min(high, through)
		}
		if last < high {
			err = deliver(high)
			continue
		}
		if meta.Sealed || through > 0 && last >= through {
			return nil
		}
		meta, err = backend.Wait(ctx, ref.Stream, last, meta.Generation)
	}
	if errors.Is(err, errFollowLimit) {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// Wait follows every armed capture while Wait performs the run's usual stop,
// summary and manifest write. Callbacks are serialized across captures.
func Wait(ctx context.Context, run *telltale.Run, follow bool, fn func(Record) error) (_ *telltale.Result, err error) {
	if !follow {
		return run.Wait()
	}
	info := run.Info()
	runtime, closeStore, err := traces.Reopen(run.Dir())
	if err != nil {
		return nil, errors.Join(err, run.Stop())
	}
	defer func() { err = errors.Join(err, closeStore()) }()
	runContext := ctx
	ctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	var group sync.WaitGroup
	var output sync.Mutex
	errs := make(chan error, len(info.Captures))
	for _, capture := range info.Captures {
		ref := Ref{RunID: info.RunID, Runtime: runtime, TraceRef: capture.Ref}
		group.Go(func() {
			followErr := Follow(ctx, ref, Query{}, func(row query.Row) error {
				output.Lock()
				defer output.Unlock()
				record, err := recordOf(ref, row)
				if err != nil {
					return err
				}
				return fn(record)
			})
			if runContext.Err() != nil && errors.Is(followErr, runContext.Err()) {
				return
			}
			if followErr != nil {
				errs <- followErr
				_ = run.Stop()
				cancel()
			}
		})
	}
	result, err := run.Wait()
	if err != nil {
		cancel()
	}
	group.Wait()
	close(errs)
	for followErr := range errs {
		err = errors.Join(err, followErr)
	}
	return result, err
}

func FollowSelection(ctx context.Context, refs []Ref, q Query, fn func(query.Row) error) error {
	if len(refs) == 0 {
		return fmt.Errorf("--follow requires a capture")
	}
	for _, ref := range refs {
		if ref.RunID != refs[0].RunID {
			return fmt.Errorf("--follow requires a single run")
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var group sync.WaitGroup
	var output sync.Mutex
	errs := make(chan error, len(refs))
	for _, ref := range refs {
		group.Go(func() {
			if err := Follow(ctx, ref, q, func(row query.Row) error { output.Lock(); defer output.Unlock(); return fn(row) }); err != nil {
				errs <- err
				cancel()
			}
		})
	}
	group.Wait()
	close(errs)
	var result error
	for err := range errs {
		result = errors.Join(result, err)
	}
	return result
}
