// Package topui is `telltale top`'s full-screen terminal view: the host's
// process groups as a tree filling the window, the host's CPU and memory
// above them, and a selected group's processes below.
package topui

import (
	"context"

	"github.com/flanksource/telltale/pkg/tracing/groups"
)

// Source reads the frames a Model shows.
type Source interface {
	// Next is the next frame; false while there is none yet, as a recording
	// that has committed no whole tick.
	Next(ctx context.Context) (groups.Frame, bool, error)
	// Processes are the processes of the latest frame filter picks; false
	// when the source keeps none, as a recording does.
	Processes(filter groups.ProcessFilter) ([]groups.ProcessSample, bool)
	Close()
}

// Live reads a Top, which keeps every process of its latest tick.
type Live struct {
	Top *groups.Top
}

func (l Live) Next(ctx context.Context) (groups.Frame, bool, error) {
	frame, err := l.Top.Tick(ctx)
	return frame, err == nil, err
}

func (l Live) Processes(filter groups.ProcessFilter) ([]groups.ProcessSample, bool) {
	return l.Top.Processes(filter), true
}

func (l Live) Close() { l.Top.Close() }

// Recorded follows a recording's committed frames, which keep no processes.
type Recorded struct {
	Follower *groups.Follower
}

func (r Recorded) Next(ctx context.Context) (groups.Frame, bool, error) {
	return r.Follower.Frame(ctx)
}

func (Recorded) Processes(groups.ProcessFilter) ([]groups.ProcessSample, bool) { return nil, false }

func (Recorded) Close() {}
