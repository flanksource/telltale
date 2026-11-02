package plancache

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/flanksource/telltale/pkg/tracing"
)

type capture struct {
	mu        sync.Mutex
	params    Params
	reader    func(context.Context, snapshotOptions) (cacheSnapshot, error)
	now       func() time.Time
	previous  map[string]CachedPlan
	sampledAt time.Time
	watermark time.Time
	polls     int
	summary   accumulator
}

func (c *capture) poll(ctx context.Context, out tracing.Emitter[Sample]) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	result, err := c.reader(ctx, snapshotOptions{Filter: Filter{Database: c.params.Databases, Text: c.params.Text}, Since: c.watermark})
	if err != nil {
		return err
	}
	if result.ServerTime.IsZero() || result.ServerTime.Before(c.watermark) {
		return fmt.Errorf("invalid plan cache server timestamp %s after %s", result.ServerTime, c.watermark)
	}
	plans := result.Plans
	at := c.now()
	current := make(map[string]CachedPlan, len(plans))
	for _, handle := range result.Handles {
		if handle == "" {
			return fmt.Errorf("empty plan handle in cache membership")
		}
		if plan, exists := c.previous[handle]; exists {
			current[handle] = plan
		}
	}
	seen := make(map[string]bool, len(plans))
	for _, p := range plans {
		if seen[p.PlanHandle] {
			return fmt.Errorf("duplicate plan handle %q in snapshot", p.PlanHandle)
		}
		seen[p.PlanHandle] = true
		if _, _, err := Delta(CachedPlan{}, p); err != nil {
			return err
		}
		current[p.PlanHandle] = p
	}
	if c.previous != nil {
		rows, err := c.samples(current)
		if err != nil {
			return err
		}
		for _, row := range rows {
			row.SampledAt, row.IntervalMs = at, float64(at.Sub(c.sampledAt))/float64(time.Millisecond)
			if err := out.Emit(ctx, row); err != nil {
				return err
			}
			c.summary.add(row)
		}
	}
	c.previous, c.sampledAt = current, at
	c.watermark = result.ServerTime
	c.polls++
	report := c.summary.result()
	report["polls"] = c.polls
	report["baseline"] = c.polls == 1
	out.Report(report)
	return nil
}

func (c *capture) samples(current map[string]CachedPlan) ([]Sample, error) {
	var rows, evictions []Sample
	for handle, plan := range current {
		row, emit, err := Delta(c.previous[handle], plan)
		if err != nil {
			return nil, err
		}
		if emit && row.Executions >= int64(c.params.MinExecutions) {
			rows = append(rows, row)
		}
	}
	slices.SortFunc(rows, compareCPU)
	top := c.params.Top
	if top == 0 {
		top = 200
	}
	if len(rows) > top {
		rows = rows[:top]
	}
	for handle, plan := range c.previous {
		if _, exists := current[handle]; exists {
			continue
		}
		row := planSample(plan)
		row.Evicted = true
		evictions = append(evictions, row)
	}
	slices.SortFunc(evictions, compareCPU)
	return append(rows, evictions...), nil
}
