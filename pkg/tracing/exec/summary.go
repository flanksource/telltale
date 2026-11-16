package exec

import "iter"

func (capture) Summarize(rows iter.Seq[ExecRecord]) (map[string]any, error) {
	count, failed := 0, 0
	duration := 0.0
	for row := range rows {
		count++
		duration += row.DurationMs
		if row.ExitCode != 0 || row.Error != "" {
			failed++
		}
	}
	return map[string]any{"executions": count, "failures": failed, "durationMs": duration}, nil
}
