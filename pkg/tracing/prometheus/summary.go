package prometheus

import "iter"

func (kind) Summarize(rows iter.Seq[Scrape]) (map[string]any, error) {
	latest := map[string]Scrape{}
	for row := range rows {
		if previous, ok := latest[row.Endpoint]; !ok || row.TakenAt.After(previous.TakenAt) {
			latest[row.Endpoint] = row
		}
	}
	up, down := 0, 0
	var samples int64
	duration := 0.0
	for _, row := range latest {
		if row.Health == "up" {
			up++
		} else {
			down++
		}
		samples += row.Samples
		duration += row.DurationMs
	}
	return map[string]any{"targets": len(latest), "up": up, "down": down, "latestSamples": samples, "latestDurationMs": duration}, nil
}
