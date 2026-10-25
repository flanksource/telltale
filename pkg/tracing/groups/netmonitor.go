package groups

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// NetMonitor runs nettop for as long as it is open, counting each process's
// network bytes since it started. nettop is costly — it holds over a core
// whatever its interval — so it runs only when network bytes are asked for.
type NetMonitor struct {
	totals *NetTotals
	cancel context.CancelFunc
	done   chan struct{}
	mu     sync.Mutex
	err    error
}

// StartNetMonitor starts nettop reporting every interval, of at least a
// second; ErrNetUnsupported where there is no nettop.
func StartNetMonitor(ctx context.Context, interval time.Duration) (*NetMonitor, error) {
	ctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	output, cmd, err := startNettop(ctx, max(time.Second, interval))
	if err != nil {
		cancel()
		return nil, err
	}
	monitor := &NetMonitor{totals: NewNetTotals(), cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(monitor.done)
		parseErr := ParseNettop(output, monitor.totals.Add)
		waitErr := cmd.Wait()
		closeErr := output.Close()
		if ctx.Err() != nil {
			return
		}
		monitor.mu.Lock()
		defer monitor.mu.Unlock()
		monitor.err = fmt.Errorf("nettop ended: %w", errors.Join(parseErr, waitErr, closeErr))
	}()
	return monitor, nil
}

// Ready is closed once nettop has counted past its baseline.
func (m *NetMonitor) Ready() <-chan struct{} { return m.totals.Ready() }

// Counters are each pid's network bytes since the monitor started, nil until
// Ready, failing once nettop has ended.
func (m *NetMonitor) Counters() (map[int]NetBytes, error) {
	m.mu.Lock()
	err := m.err
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return m.totals.Counters(), nil
}

// Close stops nettop.
func (m *NetMonitor) Close() {
	m.cancel()
	<-m.done
}
