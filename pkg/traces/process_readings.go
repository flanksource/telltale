package traces

import (
	"context"
	"fmt"

	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/process"
)

// ProcessMark is where a span of the run begins in its samples stream.
type ProcessMark struct{ Samples int64 }

// Mark commits the samples taken so far and returns where the next span
// begins.
func (p *ProcessTrace) Mark(ctx context.Context) (ProcessMark, error) {
	seq, err := p.samples.Mark(ctx)
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
	to, err := p.samples.Mark(ctx)
	if err != nil {
		return ProcessReadings{}, err
	}
	var readings ProcessReadings
	if to > after.Samples {
		if readings.Samples, err = readAll[process.Sample](ctx, p.host, p.samples.Ref(after.Samples, to)); err != nil {
			return ProcessReadings{}, err
		}
	}
	readings.Nodes, err = p.Nodes(ctx)
	return readings, err
}

// Nodes are every process of the tree as last seen, after re-observing it.
func (p *ProcessTrace) Nodes(ctx context.Context) ([]process.Node, error) {
	to, err := p.tree.Mark(ctx)
	if err != nil || to == 0 {
		return nil, err
	}
	return readAll[process.Node](ctx, p.host, p.tree.Ref(0, to))
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
