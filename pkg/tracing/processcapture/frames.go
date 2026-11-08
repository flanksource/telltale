package processcapture

import (
	"context"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/groups"
)

type Frames struct {
	HighSeq int64          `json:"highSeq"`
	Frames  []groups.Frame `json:"frames"`
}

// ReadFrames derives complete ticks from an atomic process metrics stream.
func ReadFrames(ctx context.Context, runtime *tracing.Runtime, ref tracing.TraceRef) (Frames, error) {
	result := Frames{Frames: []groups.Frame{}}
	metricRef := tracing.TraceRef{Kind: MetricsTable, Stream: ref.Stream + "-metrics", AfterSeq: ref.AfterSeq}
	meta, err := runtime.Results.Backend.Meta(ctx, metricRef.Stream)
	if err != nil {
		return result, err
	}
	metricRef.ToSeq, result.HighSeq = meta.HighSeq, meta.HighSeq
	identities := []Identity{}
	primary := tracing.TraceRef{Kind: Kind, Stream: ref.Stream}
	if err := tracing.ReadRecords(ctx, runtime, primary, func(p Identity) error { identities = append(identities, p); return nil }); err != nil {
		return result, err
	}
	var tick []MetricRow
	flush := func() error {
		if len(tick) == 0 {
			return nil
		}
		snapshot, err := GroupSnapshot(identities, tick)
		if err != nil {
			return err
		}
		result.Frames = append(result.Frames, snapshot.Frame)
		tick = tick[:0]
		return nil
	}
	if err := tracing.ReadRecords(ctx, runtime, metricRef, func(row MetricRow) error {
		if len(tick) > 0 && tick[0].Tick != row.Tick {
			if err := flush(); err != nil {
				return err
			}
		}
		tick = append(tick, row)
		return nil
	}); err != nil {
		return result, err
	}
	if err := flush(); err != nil {
		return result, err
	}
	return result, nil
}
