package traceui

import (
	"slices"
	"strconv"
	"strings"

	"github.com/flanksource/commons-db/recordstore/sampleframe"
)

func sqlTopMetrics(frame sampleframe.Frame, frames []sampleframe.Frame) []string {
	reading, _ := frame.Summary["serverMetrics"].(map[string]any)
	if reading == nil {
		return nil
	}
	metrics := []struct{ key, label, unit string }{
		{"cpuPercent", "Host CPU", "%"},
		{"readIOPS", "Read IOPS", ""},
		{"writeIOPS", "Write IOPS", ""},
		{"bufferUsagePercent", "Shared buffers used", "%"},
	}
	if frame.Summary["engine"] == "sqlserver" {
		metrics[0].label = "SQL Server CPU"
		metrics[3] = struct{ key, label, unit string }{"pageLifeExpectancySeconds", "PLE", "s"}
	}
	selected := slices.IndexFunc(frames, func(sample sampleframe.Frame) bool { return sample.ID == frame.ID })
	cells := make([]string, 0, len(metrics))
	for _, metric := range metrics {
		value, ok := reading[metric.key].(float64)
		if !ok {
			cells = append(cells, metric.label+" —")
			continue
		}
		cell := metric.label + " " + strings.TrimSuffix(strconv.FormatFloat(value, 'f', 1, 64), ".0") + metric.unit
		if selected > 0 {
			if line := sqlTopTrend(frame.Source, metric.key, frames[:selected+1]); line != "" {
				cell += " " + line
			}
		}
		cells = append(cells, cell)
	}
	return []string{cells[0] + " · " + cells[3], cells[1] + " · " + cells[2]}
}

func sqlTopTrend(source, key string, frames []sampleframe.Frame) string {
	const window = 12
	bars := []rune("▁▂▃▄▅▆▇█")
	values := make([]float64, 0, window)
	for i := len(frames) - 1; i >= 0 && len(values) < window; i-- {
		if frames[i].Source != source {
			continue
		}
		metrics, _ := frames[i].Summary["serverMetrics"].(map[string]any)
		value, ok := metrics[key].(float64)
		if !ok {
			break
		}
		values = append(values, value)
	}
	if len(values) < 2 {
		return ""
	}
	peak := slices.Max(values)
	var line strings.Builder
	for i := len(values) - 1; i >= 0; i-- {
		index := 0
		if peak > 0 {
			index = int(values[i] / peak * float64(len(bars)-1))
		}
		line.WriteRune(bars[max(0, min(index, len(bars)-1))])
	}
	return line.String()
}
