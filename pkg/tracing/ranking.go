package tracing

import (
	"cmp"
	"slices"
)

// RankedValues returns the largest weights within a single sample unit.
func RankedValues(values map[string]int64) []map[string]any {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	slices.SortFunc(names, func(a, b string) int {
		if order := cmp.Compare(values[b], values[a]); order != 0 {
			return order
		}
		return cmp.Compare(a, b)
	})
	rows := make([]map[string]any, 0, min(len(names), 10))
	for _, name := range names[:min(len(names), 10)] {
		rows = append(rows, map[string]any{"function": name, "value": values[name]})
	}
	return rows
}
