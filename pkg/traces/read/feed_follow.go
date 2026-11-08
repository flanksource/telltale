package read

import (
	"context"
	"errors"
	"github.com/flanksource/commons-db/query"
	"sync"
)

// Feed follows native sequences independently. A callback is never concurrent
// with another callback, and cancellation waits for every reader to release.
func Feed(ctx context.Context, refs []Ref, q Query, fn func(Record) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var group sync.WaitGroup
	var output sync.Mutex
	errs := make(chan error, len(refs))
	for _, ref := range refs {
		group.Go(func() {
			err := Follow(ctx, ref, q, func(row query.Row) error {
				record, err := recordOf(ref, row)
				if err != nil {
					return err
				}
				output.Lock()
				defer output.Unlock()
				return fn(record)
			})
			if err != nil && !errors.Is(err, context.Canceled) {
				errs <- err
				cancel()
			}
		})
	}
	group.Wait()
	close(errs)
	var err error
	for failure := range errs {
		err = errors.Join(err, failure)
	}
	return err
}
