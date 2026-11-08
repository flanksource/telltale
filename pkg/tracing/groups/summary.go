package groups

import "iter"

func (kind) Summarize(rows iter.Seq[GroupSample]) (map[string]any, error) {
	var tick int64
	var usage Usage
	groups := 0
	for row := range rows {
		if row.Tick < tick {
			continue
		}
		if row.Tick > tick {
			tick = row.Tick
			usage = Usage{}
			groups = 0
		}
		groups++
		usage.merge(row.Self)
	}
	result := map[string]any{"groups": groups, "processes": usage.Processes, "rssBytes": usage.RSSBytes}
	if usage.CPUPercent != nil {
		result["cpuPercent"] = *usage.CPUPercent
	}
	if usage.SwapBytes != nil {
		result["swapBytes"] = *usage.SwapBytes
	}
	return result, nil
}
