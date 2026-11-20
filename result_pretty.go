package telltale

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/flanksource/clicky/api"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

// Pretty is the run on one line, then a line per capture: its target, the
// scalar entries of its summary and the command that reads its records back,
// then a line per reason the run failed. --format json keeps every field.
func (r Result) Pretty() api.Text {
	text := api.Text{}.Append("Run ", "text-muted").Append(r.RunID, "font-bold").Append(" · "+r.EndedAt.Sub(r.StartedAt).Round(r.precision()).String(), "text-muted")
	switch {
	case len(r.Command) > 0 && r.ExitCode != 0:
		text = text.Append(fmt.Sprintf(" · %s exited %d", strings.Join(r.Command, " "), r.ExitCode), "text-red-600")
	case len(r.Command) > 0:
		text = text.Append(fmt.Sprintf(" · %s exited 0", strings.Join(r.Command, " ")), "text-muted")
	case r.PID != 0:
		text = text.Append(fmt.Sprintf(" · pid %d", r.PID), "text-muted")
	}
	for _, capture := range r.Captures {
		text = text.Append("\n  ").Append(capture.Kind, "font-bold")
		for _, part := range append(targetParts(capture.Target), summaryParts(capture.Summary)...) {
			text = text.Append(" · " + part)
		}
		text = text.Append(fmt.Sprintf(" · telltale records %s --run %s", capture.Kind, r.RunID), "text-muted")
	}
	for _, failure := range append(slices.Clone(r.CheckpointErrors), r.Error) {
		if failure != "" {
			text = text.Append("\n  ✗ "+failure, "text-red-600")
		}
	}
	return text
}

// precision rounds a sub-second run to milliseconds and a longer one to a tenth of a second.
func (r Result) precision() time.Duration {
	if r.EndedAt.Sub(r.StartedAt) < time.Second {
		return time.Millisecond
	}
	return 100 * time.Millisecond
}

func targetParts(t target.Target) []string {
	switch {
	case t.Agent != "":
		return []string{"agent " + t.Agent}
	case t.PID != 0:
		return []string{fmt.Sprintf("pid %d", t.PID)}
	case len(t.Selector) > 0:
		pairs := []string{}
		for _, key := range slices.Sorted(maps.Keys(t.Selector)) {
			pairs = append(pairs, key+"="+t.Selector[key])
		}
		return []string{"agents " + strings.Join(pairs, ",")}
	case t.Seam != "":
		return []string{string(t.Seam)}
	}
	return nil
}

// summaryParts are a summary's numbers, strings and booleans by name; its
// lists and maps are left to --format json. A count is the records recorded.
func summaryParts(summary map[string]any) []string {
	parts := []string{}
	for _, key := range slices.Sorted(maps.Keys(summary)) {
		switch value := summary[key].(type) {
		case string, bool, int, int32, int64, float32, float64, uint, uint32, uint64:
			if key == "count" {
				parts = append(parts, fmt.Sprintf("%v records", value))
			} else {
				parts = append(parts, fmt.Sprintf("%s %v", key, value))
			}
		}
	}
	return parts
}
