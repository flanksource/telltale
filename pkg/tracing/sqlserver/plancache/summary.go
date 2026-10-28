package plancache

import (
	"cmp"
	"iter"
	"slices"
	"time"
)

type accumulator struct {
	plans      map[string]Sample
	polls      map[time.Time]struct{}
	recompiled int
	evicted    int
}

func (a *accumulator) add(row Sample) {
	if a.plans == nil {
		a.plans = map[string]Sample{}
		a.polls = map[time.Time]struct{}{}
	}
	a.polls[row.SampledAt] = struct{}{}
	if row.Recompiled {
		a.recompiled++
	}
	if row.Evicted {
		a.evicted++
		return
	}
	prev := a.plans[row.PlanHandle]
	row.Executions += prev.Executions
	row.CPUMs += prev.CPUMs
	row.ElapsedMs += prev.ElapsedMs
	row.LogicalReads += prev.LogicalReads
	row.PhysicalReads += prev.PhysicalReads
	row.Writes += prev.Writes
	row.Rows += prev.Rows
	if row.Executions > 0 {
		row.AvgCPUMs = row.CPUMs / float64(row.Executions)
		row.AvgElapsedMs = row.ElapsedMs / float64(row.Executions)
	}
	a.plans[row.PlanHandle] = row
}

func compareCPU(a, b Sample) int {
	if order := cmp.Compare(b.CPUMs, a.CPUMs); order != 0 {
		return order
	}
	return cmp.Compare(a.PlanHandle, b.PlanHandle)
}

func (a *accumulator) result() map[string]any {
	plans := make([]Sample, 0, len(a.plans))
	for _, row := range a.plans {
		plans = append(plans, row)
	}
	slices.SortFunc(plans, compareCPU)
	cpu := slices.Clone(plans[:min(10, len(plans))])
	slices.SortFunc(plans, func(a, b Sample) int {
		if order := cmp.Compare(b.ElapsedMs, a.ElapsedMs); order != 0 {
			return order
		}
		return cmp.Compare(a.PlanHandle, b.PlanHandle)
	})
	return map[string]any{"topCPU": cpu, "topElapsed": plans[:min(10, len(plans))], "recompiled": a.recompiled, "evicted": a.evicted, "polls": len(a.polls)}
}

// Summarize counts polls represented in this record window; the live Report also
// counts the baseline and polls with no emitted records.
func (handler) Summarize(rows iter.Seq[Sample]) (map[string]any, error) {
	var summary accumulator
	for row := range rows {
		summary.add(row)
	}
	return summary.result(), nil
}
