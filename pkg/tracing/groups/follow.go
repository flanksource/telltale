package groups

import (
	"context"
	"fmt"

	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/telltale/pkg/tracing"
)

// Follower reads a process_groups capture's frames back as they are
// committed, so a view of a recorded run shows what its store holds.
type Follower struct {
	runtime *tracing.Runtime
	ref     tracing.TraceRef
	after   int64
	ticks   map[int64][]GroupSample
	latest  int64
}

// NewFollower follows the capture's stream that ref names, from its start.
func NewFollower(runtime *tracing.Runtime, ref tracing.TraceRef) (*Follower, error) {
	if ref.Kind != Kind {
		return nil, fmt.Errorf("follow %s: a %s capture is no %s capture", ref.Stream, ref.Kind, Kind)
	}
	return &Follower{runtime: runtime, ref: ref, ticks: map[int64][]GroupSample{}}, nil
}

// Frame is the latest whole tick committed: while rows of a newer tick keep
// arriving, the tick before it, whose rows are all in; false before any.
func (f *Follower) Frame(ctx context.Context) (Frame, bool, error) {
	high := f.after
	window := f.ref
	window.AfterSeq, window.ToSeq = f.after, 0
	if err := f.runtime.Read(ctx, window, func(seq int64, _ recordstore.Row) error {
		high = seq
		return nil
	}); err != nil {
		return Frame{}, false, err
	}
	arrived := high > f.after
	if arrived {
		window.ToSeq = high
		if err := tracing.ReadRecords(ctx, f.runtime, window, func(sample GroupSample) error {
			f.ticks[sample.Tick] = append(f.ticks[sample.Tick], sample)
			f.latest = max(f.latest, sample.Tick)
			return nil
		}); err != nil {
			return Frame{}, false, err
		}
		f.after = high
	}
	shown := f.latest
	if arrived {
		shown--
	}
	for tick := range f.ticks {
		if tick < shown {
			delete(f.ticks, tick)
		}
	}
	samples, ok := f.ticks[shown]
	if !ok {
		return Frame{}, false, nil
	}
	return Frame{SampledAt: samples[0].SampledAt, Tick: shown, Groups: samples}, true, nil
}
