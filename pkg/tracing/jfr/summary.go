package jfr

import (
	"github.com/flanksource/telltale/pkg/tracing"
	"iter"
)

func (b builtin) Summarize(rows iter.Seq[Sample]) (map[string]any, error) {
	return b.native.Summarize(rows)
}

func (profiler[P]) Summarize(rows iter.Seq[Sample]) (map[string]any, error) {
	events := map[string]int64{}
	totals := map[string]int64{}
	functions := map[string]map[string]int64{}
	for row := range rows {
		events[row.Event] += row.Samples
		totals[row.Event+"/"+row.Unit] += row.Value
		key := row.Event + "/" + row.Unit
		if functions[key] == nil {
			functions[key] = map[string]int64{}
		}
		functions[key][row.Frame] += row.Value
	}
	ranked := map[string]any{}
	for key, values := range functions {
		ranked[key] = tracing.RankedValues(values)
	}
	return map[string]any{"events": events, "totals": totals, "functions": ranked}, nil
}
