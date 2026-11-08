package shutdown

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const GracePeriod = 30 * time.Second

type Options struct {
	GracePeriod time.Duration
	Signals     <-chan os.Signal
	Output      io.Writer
	ForceExit   func(int)
	Restore     func(bool)
}

type contextKey struct{}

type Controller struct {
	ctx         context.Context
	cancel      context.CancelFunc
	options     Options
	request     chan struct{}
	done        chan struct{}
	stopped     chan struct{}
	closeOnce   sync.Once
	requestOnce sync.Once
	terminal    atomic.Bool
}

func New(parent context.Context, options Options) (*Controller, error) {
	if options.GracePeriod <= 0 || options.Signals == nil || options.Output == nil || options.ForceExit == nil || options.Restore == nil {
		return nil, errors.New("shutdown requires a positive grace period, signal channel, output, exit and terminal restore functions")
	}
	ctx, cancel := context.WithCancel(parent)
	c := &Controller{cancel: cancel, options: options, request: make(chan struct{}, 1), done: make(chan struct{}), stopped: make(chan struct{})}
	c.ctx = context.WithValue(ctx, contextKey{}, c)
	go c.watch(parent)
	return c, nil
}

func (c *Controller) Context() context.Context { return c.ctx }

func (c *Controller) Close() {
	c.closeOnce.Do(func() { close(c.done); c.cancel() })
	<-c.stopped
}

func (c *Controller) watch(parent context.Context) {
	defer close(c.stopped)
	select {
	case <-c.done:
		return
	case _, open := <-c.options.Signals:
		if !open {
			c.force(1, "shutdown signal channel closed unexpectedly")
			return
		}
	case <-c.request:
	case <-parent.Done():
	}
	c.cancel()
	timer := time.NewTimer(c.options.GracePeriod)
	defer timer.Stop()
	_, _ = fmt.Fprintln(c.options.Output, "Shutting down; press Ctrl+C again to force exit.")
	select {
	case <-c.done:
	case sig, open := <-c.options.Signals:
		if !open {
			c.force(1, "shutdown signal channel closed unexpectedly")
			return
		}
		code := 1
		if value, ok := sig.(syscall.Signal); ok {
			code = 128 + int(value)
		}
		c.force(code, "Forced exit.")
	case <-timer.C:
		c.force(1, fmt.Sprintf("Shutdown exceeded %s; exiting with incomplete cleanup.", c.options.GracePeriod))
	}
}

func (c *Controller) force(code int, message string) {
	select {
	case <-c.done:
		return
	default:
	}
	c.options.Restore(c.terminal.Load())
	_, _ = fmt.Fprintln(c.options.Output, message)
	c.options.ForceExit(code)
}

// Request starts the same graceful shutdown used by an OS signal.
func Request(ctx context.Context) error {
	c, ok := ctx.Value(contextKey{}).(*Controller)
	if !ok {
		return errors.New("command shutdown controller is missing")
	}
	c.requestOnce.Do(func() { c.request <- struct{}{} })
	return nil
}

// Terminal tracks an alternate-screen view until its Run returns.
func Terminal(ctx context.Context) (func(), error) {
	c, ok := ctx.Value(contextKey{}).(*Controller)
	if !ok {
		return nil, errors.New("command shutdown controller is missing")
	}
	c.terminal.Store(true)
	return func() { c.terminal.Store(false) }, nil
}
