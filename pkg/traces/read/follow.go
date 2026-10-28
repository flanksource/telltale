package read

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/commons-db/recordstore"
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
	dbctx, p, err := ref.profile(ctx, "")
	if err != nil {
		return err
	}
	params, err := q.params(ref, p)
	if err != nil {
		return err
	}
	through := ref.ToSeq
	if q.ToSeq > 0 && (through == 0 || q.ToSeq < through) {
		through = q.ToSeq
	}
	if through > 0 && max(ref.AfterSeq, q.AfterSeq) >= through {
		return nil
	}
	count := 0
	// Query every notified seq through the original profile, preserving closing
	// time/filter edges that query.Follow deliberately opens for rolling views.
	deliver := func(seq int64) error {
		if through > 0 && seq > through {
			return errFollowLimit
		}
		window := q
		window.AfterSeq = seq - 1
		window.ToSeq = seq
		window.All = true
		window.Cursor = ""
		window.NextCursor = nil
		for row, err := range Rows(ctx, []Ref{ref}, window) {
			if err != nil {
				return err
			}
			if err := fn(row); err != nil {
				return err
			}
			count++
			if q.Limit > 0 && count >= q.Limit {
				return errFollowLimit
			}
		}
		if through > 0 && seq >= through {
			return errFollowLimit
		}
		return nil
	}
	if !query.SupportsStreaming(p.Provider.Type) {
		err = ref.Runtime.Results.Backend.Tail(ctx, ref.Stream, max(ref.AfterSeq, q.AfterSeq), func(seq int64, _ recordstore.Row) error { return deliver(seq) })
	} else {
		followCtx, cancel := dbctx.WithCancel()
		defer cancel()
		// The notification stream carries every seq; filtering stays in deliver.
		window := Query{AfterSeq: q.AfterSeq, ToSeq: q.ToSeq}
		params, err = window.params(ref, p)
		if err != nil {
			return err
		}
		p = query.Follow(p)
		p.Trace.Buffer = nil
		session, err := query.ExecuteStream(followCtx, ref.Runtime.Sessions, p, params)
		if err != nil {
			return err
		}
		defer func() { cancel(); <-session.Done() }()
		replay, live, unsubscribe := session.Subscribe()
		defer unsubscribe()
		var previous int64
		consume := func(event query.Event) error {
			if event.Sequence != previous+1 {
				return fmt.Errorf("follow subscriber lost events after %d (received %d)", previous, event.Sequence)
			}
			previous = event.Sequence
			if event.Error != "" {
				return errors.New(event.Error)
			}
			if event.Row == nil {
				return nil
			}
			seq, ok := event.Row["seq"].(int64)
			if !ok {
				return fmt.Errorf("follow %s: expected int64 seq, got %T", ref.Kind, event.Row["seq"])
			}
			return deliver(seq)
		}
		for _, event := range replay {
			if err = consume(event); err != nil {
				break
			}
		}
		if err == nil {
			for event := range live {
				if err = consume(event); err != nil {
					break
				}
			}
		}
		if err == nil && session.Snapshot().Error != "" {
			err = errors.New(session.Snapshot().Error)
		}
		if err == nil && session.Snapshot().EventCount != previous {
			err = fmt.Errorf("follow subscriber lost trailing events after %d (produced %d)", previous, session.Snapshot().EventCount)
		}
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
func Wait(ctx context.Context, run *telltale.Run, follow bool, fn func(query.Row) error) (_ *telltale.Result, err error) {
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
	ctx, cancel := context.WithCancel(ctx)
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
				if len(info.Captures) > 1 {
					row["kind"] = ref.Kind
				}
				return fn(row)
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
