package spans

import (
	telltale "github.com/flanksource/telltale/sdk"
	"iter"
)

func (capture) Summarize(rows iter.Seq[telltale.Span]) (map[string]any, error) {
	statuses := map[string]int{}
	traces := map[string]bool{}
	duration := 0.0
	for row := range rows {
		statuses[row.Status]++
		traces[row.TraceID] = true
		duration += row.DurationMs
	}
	return map[string]any{"traces": len(traces), "statuses": statuses, "durationMs": duration}, nil
}
