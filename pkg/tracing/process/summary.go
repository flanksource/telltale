package process

import (
	"fmt"
	"iter"
	"math"
	"math/rand/v2"
	"slices"
)

const summaryReservoir = 8192
const summaryProcesses = 1024

type sampleMetric struct {
	count         int64
	min, max, avg float64
	values        []float64
}

func (m *sampleMetric) add(value float64, rng *rand.Rand) {
	m.count++
	if m.count == 1 {
		m.min = value
		m.max = value
		m.values = make([]float64, 0, summaryReservoir)
	}
	m.min = math.Min(m.min, value)
	m.max = math.Max(m.max, value)
	m.avg += (value - m.avg) / float64(m.count)
	if len(m.values) < summaryReservoir {
		m.values = append(m.values, value)
	} else if index := rng.Int64N(m.count); index < summaryReservoir {
		m.values[index] = value
	}
}

func (m *sampleMetric) summary() map[string]any {
	result := map[string]any{"count": m.count, "min": nil, "avg": nil, "max": nil, "p95": nil, "approximate": m.count > summaryReservoir}
	if m.count > 0 {
		slices.Sort(m.values)
		result["min"], result["avg"], result["max"] = m.min, m.avg, m.max
		result["p95"] = m.values[int(math.Ceil(float64(len(m.values))*.95))-1]
	}
	return result
}

type diskCounter struct {
	first, last uint64
	seen        bool
}

func (c *diskCounter) add(value *uint64) error {
	if value == nil {
		return nil
	}
	if c.seen && *value < c.last {
		return fmt.Errorf("disk counter decreased from %d to %d", c.last, *value)
	}
	if !c.seen {
		c.first = *value
		c.seen = true
	}
	c.last = *value
	return nil
}
func (c diskCounter) delta() any {
	if !c.seen {
		return nil
	}
	return c.last - c.first
}

type processSummary struct {
	pid         int
	count       int64
	cpu, rss    sampleMetric
	read, write diskCounter
}

// Summarize separates process identities so PID reuse cannot mix counters.
// Each metric keeps at most 8192 values; at most 1024 process identities are
// accepted (128 MiB of reservoir values). Percentiles above the cap are sampled.
func (samples) Summarize(rows iter.Seq[Sample]) (map[string]any, error) {
	processes := map[string]*processSummary{}
	var order []string
	rng := rand.New(rand.NewPCG(1, 2))
	for row := range rows {
		if row.PID <= 0 || row.Key == "" {
			return nil, fmt.Errorf("sample requires a positive pid and process key")
		}
		p := processes[row.Key]
		if p == nil {
			if len(processes) == summaryProcesses {
				return nil, fmt.Errorf("process summary exceeds %d identities", summaryProcesses)
			}
			p = &processSummary{pid: row.PID}
			processes[row.Key] = p
			order = append(order, row.Key)
		}
		if p.pid != row.PID {
			return nil, fmt.Errorf("process key %s changed pid", row.Key)
		}
		p.count++
		if row.CPUPercent != nil {
			if math.IsNaN(*row.CPUPercent) || math.IsInf(*row.CPUPercent, 0) {
				return nil, fmt.Errorf("pid %d has nonfinite CPU", row.PID)
			}
			p.cpu.add(*row.CPUPercent, rng)
		}
		p.rss.add(float64(row.RSSBytes), rng)
		if err := p.read.add(row.DiskReadBytes); err != nil {
			return nil, fmt.Errorf("pid %d read: %w", row.PID, err)
		}
		if err := p.write.add(row.DiskWriteBytes); err != nil {
			return nil, fmt.Errorf("pid %d write: %w", row.PID, err)
		}
	}
	result := []map[string]any{}
	for _, key := range order {
		p := processes[key]
		result = append(result, map[string]any{"pid": p.pid, "key": key, "sampleCount": p.count, "cpuPercent": p.cpu.summary(), "rssBytes": p.rss.summary(), "diskReadBytesDelta": p.read.delta(), "diskWriteBytesDelta": p.write.delta()})
	}
	return map[string]any{"processes": result}, nil
}
