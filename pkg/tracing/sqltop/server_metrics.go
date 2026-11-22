package sqltop

import (
	"maps"
	"time"
)

const (
	cpuPercent        = "cpuPercent"
	readIOPS          = "readIOPS"
	writeIOPS         = "writeIOPS"
	pageLifeSeconds   = "pageLifeExpectancySeconds"
	bufferUsedPercent = "bufferUsagePercent"
	awaitingSample    = "Waiting for next sample"
)

type serverReading struct {
	sqlServerCPU       *sqlServerCPUCounters
	cpuTicks           *[5]int64
	postgresStartedAt  time.Time
	io                 ioCounters
	pageLifeSeconds    *int64
	bufferUsagePercent *float64
	unavailable        map[string]string
}

func (r *serverReading) unavailableFor(reason string, metrics ...string) {
	if r.unavailable == nil {
		r.unavailable = make(map[string]string)
	}
	for _, metric := range metrics {
		r.unavailable[metric] = reason
	}
}

func (r *serverReading) summary(previous *serverReading) map[string]any {
	values := make(map[string]any)
	unavailable := make(map[string]string)
	maps.Copy(unavailable, r.unavailable)
	switch {
	case r.sqlServerCPU != nil:
		if percent, ok := r.sqlServerCPU.percent(previous); ok {
			values[cpuPercent] = percent
		} else {
			unavailable[cpuPercent] = awaitingSample
		}
	case r.cpuTicks != nil:
		if percent, ok := r.postgresCPU(previous); ok {
			values[cpuPercent] = percent
		} else {
			unavailable[cpuPercent] = awaitingSample
		}
	}
	if r.io.files != nil {
		if reads, writes, ok := r.io.rates(previous); ok {
			values[readIOPS], values[writeIOPS] = reads, writes
		} else {
			unavailable[readIOPS], unavailable[writeIOPS] = awaitingSample, awaitingSample
		}
	}
	if r.pageLifeSeconds != nil {
		values[pageLifeSeconds] = *r.pageLifeSeconds
	}
	if r.bufferUsagePercent != nil {
		values[bufferUsedPercent] = *r.bufferUsagePercent
	}
	if len(unavailable) > 0 {
		values["unavailable"] = unavailable
	}
	return values
}

func (r *serverReading) postgresCPU(previous *serverReading) (float64, bool) {
	if previous == nil || previous.cpuTicks == nil || !previous.postgresStartedAt.Equal(r.postgresStartedAt) {
		return 0, false
	}
	var busy, total float64
	for i, ticks := range r.cpuTicks {
		delta := ticks - previous.cpuTicks[i]
		if delta < 0 {
			return 0, false
		}
		total += float64(delta)
		if i < 3 { // user, nice, system
			busy += float64(delta)
		}
	}
	if total <= 0 {
		return 0, false
	}
	return 100 * busy / total, true
}

type fileKey struct {
	database, file int64
	handle         string
}

type operations struct{ reads, writes int64 }

type sqlServerCPUCounters struct {
	at, startedAt, processMS, cpus int64
}

func (c sqlServerCPUCounters) percent(previous *serverReading) (float64, bool) {
	if previous == nil || previous.sqlServerCPU == nil {
		return 0, false
	}
	prior := previous.sqlServerCPU
	if c.startedAt != prior.startedAt || c.at <= prior.at || c.processMS < prior.processMS || c.cpus <= 0 || c.cpus != prior.cpus {
		return 0, false
	}
	capacity := float64(c.at-prior.at) * float64(c.cpus)
	return min(100, 100*float64(c.processMS-prior.processMS)/capacity), true
}

type ioCounters struct {
	at, reset time.Time
	files     map[fileKey]operations
}

func (c ioCounters) delta(previous ioCounters) *operations {
	if !c.reset.Equal(previous.reset) || len(c.files) != len(previous.files) {
		return nil
	}
	var delta operations
	for file, current := range c.files {
		prior, ok := previous.files[file]
		if !ok || current.reads < prior.reads || current.writes < prior.writes {
			return nil
		}
		delta.reads += current.reads - prior.reads
		delta.writes += current.writes - prior.writes
	}
	return &delta
}

func (c ioCounters) rates(previous *serverReading) (reads, writes float64, ok bool) {
	if previous == nil || !c.at.After(previous.io.at) {
		return 0, 0, false
	}
	delta := c.delta(previous.io)
	if delta == nil {
		return 0, 0, false
	}
	seconds := c.at.Sub(previous.io.at).Seconds()
	return float64(delta.reads) / seconds, float64(delta.writes) / seconds, true
}
