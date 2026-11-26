package livestream

import (
	"context"
	"errors"
	"sync"
	"time"
)

type Sampler[K comparable, V any] struct {
	mu     sync.Mutex
	closed bool
	scopes map[K]*sampleScope[V]
}

type sampleScope[V any] struct {
	context.Context
	cancel   context.CancelFunc
	done     chan struct{}
	wake     chan struct{}
	readers  int
	revision uint64
	value    V
	err      error
}

// Follow shares one sampler per key and only retains its newest sample.
func (s *Sampler[K, V]) Follow(ctx context.Context, key K, load func(context.Context) (V, time.Duration, error), emit func(V) error) error {
	scope, err := s.subscribe(ctx, key, load)
	if err != nil {
		return err
	}
	defer s.unsubscribe(scope)
	var last uint64
	for {
		s.mu.Lock()
		value, revision, wake, failure := scope.value, scope.revision, scope.wake, scope.err
		s.mu.Unlock()
		if failure != nil {
			return failure
		}
		if revision != last {
			if err := emit(value); err != nil {
				return err
			}
			last = revision
		}
		select {
		case <-scope.Done():
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-wake:
		}
	}
}

func (s *Sampler[K, V]) subscribe(ctx context.Context, key K, load func(context.Context) (V, time.Duration, error)) (*sampleScope[V], error) {
	for {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return nil, errors.New("sampler is closed")
		}
		if scope := s.scopes[key]; scope != nil {
			if scope.Err() == nil {
				scope.readers++
				s.mu.Unlock()
				return scope, nil
			}
			s.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-scope.done:
				continue
			}
		}
		if s.scopes == nil {
			s.scopes = make(map[K]*sampleScope[V])
		}
		shared, cancel := context.WithCancel(context.WithoutCancel(ctx))
		scope := &sampleScope[V]{Context: shared, cancel: cancel, done: make(chan struct{}), wake: make(chan struct{}), readers: 1}
		s.scopes[key] = scope
		s.mu.Unlock()
		go s.sample(key, scope, load)
		return scope, nil
	}
}

func (s *Sampler[K, V]) unsubscribe(scope *sampleScope[V]) {
	s.mu.Lock()
	defer s.mu.Unlock()
	scope.readers--
	if scope.readers == 0 {
		scope.cancel()
	}
}

func (s *Sampler[K, V]) sample(key K, scope *sampleScope[V], load func(context.Context) (V, time.Duration, error)) {
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.scopes, key)
		close(scope.done)
	}()
	for scope.Err() == nil {
		value, delay, err := load(scope)
		if err == nil && delay <= 0 {
			err = errors.New("sampler must return a positive interval")
		}
		s.mu.Lock()
		scope.value, scope.err = value, err
		scope.revision++
		close(scope.wake)
		scope.wake = make(chan struct{})
		s.mu.Unlock()
		if err != nil {
			return
		}
		timer := time.NewTimer(delay)
		select {
		case <-scope.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (s *Sampler[K, V]) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for _, scope := range s.scopes {
		scope.cancel()
	}
}
