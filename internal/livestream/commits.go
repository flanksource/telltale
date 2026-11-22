package livestream

import (
	"context"

	"github.com/flanksource/commons-db/recordstore"
	"golang.org/x/sync/errgroup"
)

type CommitSource func(context.Context, func()) error

// Watch reads once and then after commits. Several changes can coalesce into one read.
func Watch(ctx context.Context, sources []CommitSource, read func() error) error {
	ctx, cancel := context.WithCancel(ctx)
	group, watch := errgroup.WithContext(ctx)
	changed := make(chan struct{}, 1)
	notify := func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	}
	for _, source := range sources {
		group.Go(func() error { return source(watch, notify) })
	}
	done := make(chan error, 1)
	go func() { done <- group.Wait(); close(done) }()
	defer func() { cancel(); <-done }()
	if err := read(); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-done:
			if err != nil {
				return err
			}
			select {
			case <-changed:
				return read()
			default:
				return nil
			}
		case <-changed:
			if err := read(); err != nil {
				return err
			}
		}
	}
}

func Commits(ctx context.Context, backend *recordstore.Notifier, stream string, commit func(recordstore.Meta) error) error {
	meta, err := backend.Meta(ctx, stream)
	if err != nil {
		return err
	}
	for {
		if err := commit(meta); err != nil || meta.Sealed {
			return err
		}
		meta, err = backend.Wait(ctx, stream, meta.HighSeq, meta.Generation)
		if err != nil {
			return err
		}
	}
}
