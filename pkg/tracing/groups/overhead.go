package groups

import "time"

// Overhead is what reading a tick cost the telltale reading it: the CPU it
// and the commands it ran and waited on (ps, lsof, launchctl) used since the
// tick before, of one core, and how long the tick took. What it runs and
// keeps running, such as nettop, shows in its SelfGroup group.
type Overhead struct {
	CPUPercent *float64 `json:"cpuPercent,omitempty"`
	TickMillis float64  `json:"tickMillis"`
}

// overheadReader rates the CPU time used between ticks.
type overheadReader struct {
	cpu time.Duration
	at  time.Time
}

// read is the overhead of a tick begun at started and ended at ended.
func (r *overheadReader) read(started, ended time.Time) (Overhead, error) {
	cpu, err := cpuTime()
	if err != nil {
		return Overhead{}, err
	}
	overhead := Overhead{TickMillis: float64(ended.Sub(started)) / float64(time.Millisecond)}
	if wall := ended.Sub(r.at); !r.at.IsZero() && wall > 0 {
		percent := 100 * float64(cpu-r.cpu) / float64(wall)
		overhead.CPUPercent = &percent
	}
	r.cpu, r.at = cpu, ended
	return overhead, nil
}
