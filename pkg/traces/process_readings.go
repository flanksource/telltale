package traces

import (
	"context"
	"fmt"

	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/process"
	"github.com/flanksource/telltale/pkg/tracing/processcapture"
)

// ProcessMark is where a span of the run begins in its samples stream.
type ProcessMark struct{ Samples int64 }

// Mark commits the samples taken so far and returns where the next span
// begins.
func (p *ProcessTrace) Mark(ctx context.Context) (ProcessMark, error) {
	if err := p.CheckpointTree(ctx); err != nil {
		return ProcessMark{}, err
	}
	seq, err := p.host.highSeq(ctx, p.Refs().Samples.Stream)
	return ProcessMark{Samples: seq}, err
}

// ProcessReadings are the samples of a span of the run and every process of
// the tree as last seen.
type ProcessReadings struct {
	Samples []process.Sample
	Nodes   []process.Node
}

// Readings are the samples taken after mark through now, and the tree.
func (p *ProcessTrace) Readings(ctx context.Context, after ProcessMark) (ProcessReadings, error) {
	mark, err := p.Mark(ctx)
	if err != nil {
		return ProcessReadings{}, err
	}
	var readings ProcessReadings
	refs := p.Refs()
	identities, err := readAll[processcapture.Identity](ctx, p.host, refs.Tree)
	if err != nil {
		return readings, err
	}
	refs.Samples.AfterSeq, refs.Samples.ToSeq = after.Samples, mark.Samples
	if mark.Samples <= after.Samples {
		readings.Nodes = processcapture.Nodes(identities, nil)
		return readings, nil
	}
	metrics, err := readAll[processcapture.MetricRow](ctx, p.host, refs.Samples)
	if err != nil {
		return readings, err
	}
	readings.Nodes, readings.Samples = processcapture.Nodes(identities, metrics), processcapture.Samples(identities, metrics)
	return readings, nil
}

// Nodes are every process of the tree as last seen, after re-observing it.
func (p *ProcessTrace) Nodes(ctx context.Context) ([]process.Node, error) {
	if err := p.CheckpointTree(ctx); err != nil {
		return nil, err
	}
	refs := p.Refs()
	identities, err := readAll[processcapture.Identity](ctx, p.host, refs.Tree)
	if err != nil {
		return nil, err
	}
	metrics, err := readAll[processcapture.MetricRow](ctx, p.host, refs.Samples)
	if err != nil {
		return nil, err
	}
	return processcapture.Nodes(identities, metrics), nil
}

func readAll[R any](ctx context.Context, host *Host, ref tracing.TraceRef) ([]R, error) {
	var records []R
	err := tracing.ReadRecords(ctx, host.runtime, ref, func(record R) error {
		records = append(records, record)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read %s records: %w", ref.Kind, err)
	}
	return records, nil
}

// ProcessUsage is what one process used over a span of the run's samples:
// its key in the tree, and its peak CPU% and RSS. A process the span sampled
// no tick of has no key.
type ProcessUsage struct {
	Key            string
	PeakCPUPercent float64
	PeakRSSBytes   uint64
}

// Usage is pid's usage over the readings' samples.
func (r ProcessReadings) Usage(pid int) ProcessUsage {
	var usage ProcessUsage
	for _, sample := range r.Samples {
		if sample.PID != pid {
			continue
		}
		if usage.Key == "" {
			usage.Key = sample.Key
		}
		if sample.CPUPercent != nil {
			usage.PeakCPUPercent = max(usage.PeakCPUPercent, *sample.CPUPercent)
		}
		usage.PeakRSSBytes = max(usage.PeakRSSBytes, sample.RSSBytes)
	}
	return usage
}
