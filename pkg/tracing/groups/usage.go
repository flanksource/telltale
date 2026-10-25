package groups

import (
	"time"
)

// PerSecond are a process's or group's disk and network rates, in bytes a
// second; nil where unknown, such as on a process's first observation.
type PerSecond struct {
	DiskRead  *float64 `json:"diskReadBps,omitempty" pretty:"label=Disk read/s,type=bytes"`
	DiskWrite *float64 `json:"diskWriteBps,omitempty" pretty:"label=Disk write/s,type=bytes"`
	NetRx     *float64 `json:"netRxBps,omitempty" pretty:"label=Net in/s,type=bytes"`
	NetTx     *float64 `json:"netTxBps,omitempty" pretty:"label=Net out/s,type=bytes"`
}

// Reading is one process's use at one tick.
type Reading struct {
	CPUPercent *float64
	RSSBytes   uint64
	PerSecond
	Sockets, Connections *int
}

// Usage is what a set of processes use at one tick: the sums of what is
// known of them, nil where nothing is.
type Usage struct {
	Processes  int      `json:"processes"`
	CPUPercent *float64 `json:"cpuPercent,omitempty" pretty:"label=CPU %"`
	RSSBytes   uint64   `json:"rssBytes" pretty:"label=RSS,type=bytes"`
	PerSecond
	Sockets     *int `json:"sockets,omitempty"`
	Connections *int `json:"connections,omitempty"`
}

func (u *Usage) add(r Reading) {
	u.Processes++
	u.CPUPercent = sum(u.CPUPercent, r.CPUPercent)
	u.RSSBytes += r.RSSBytes
	u.PerSecond = u.PerSecond.add(r.PerSecond)
	u.Sockets, u.Connections = sumInt(u.Sockets, r.Sockets), sumInt(u.Connections, r.Connections)
}

func (u *Usage) merge(other Usage) {
	u.Processes += other.Processes
	u.CPUPercent = sum(u.CPUPercent, other.CPUPercent)
	u.RSSBytes += other.RSSBytes
	u.PerSecond = u.PerSecond.add(other.PerSecond)
	u.Sockets, u.Connections = sumInt(u.Sockets, other.Sockets), sumInt(u.Connections, other.Connections)
}

func (p PerSecond) add(other PerSecond) PerSecond {
	return PerSecond{
		DiskRead: sum(p.DiskRead, other.DiskRead), DiskWrite: sum(p.DiskWrite, other.DiskWrite),
		NetRx: sum(p.NetRx, other.NetRx), NetTx: sum(p.NetTx, other.NetTx),
	}
}

func sum(a, b *float64) *float64 {
	if a == nil || b == nil {
		if a == nil {
			return clone(b)
		}
		return a
	}
	total := *a + *b
	return &total
}

func sumInt(a, b *int) *int {
	if a == nil || b == nil {
		if a == nil {
			return clone(b)
		}
		return a
	}
	total := *a + *b
	return &total
}

func clone[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

// Totals are a group's own processes' use, and that with every group under
// it.
type Totals struct {
	Self  Usage `json:"self"`
	Total Usage `json:"total"`
}

// Aggregate is the Totals of each group, by key, from each process's
// reading by process key; a process without one counts with nothing known.
func Aggregate(classified []Group, readings map[string]Reading) map[string]Totals {
	totals := make(map[string]Totals, len(classified))
	for _, group := range classified {
		var self Usage
		for _, member := range group.Members {
			self.add(readings[keyOf(member)])
		}
		totals[group.Key] = Totals{Self: self, Total: self}
	}
	// Classify lists parents before children, so adding each group's total to
	// its parent's from the last back reaches every ancestor.
	for index := len(classified) - 1; index >= 0; index-- {
		group := classified[index]
		parent, ok := totals[group.ParentKey]
		if group.ParentKey == "" || !ok {
			continue
		}
		parent.Total.merge(totals[group.Key].Total)
		totals[group.ParentKey] = parent
	}
	return totals
}

// Counters are a process's cumulative byte counters; nil where unread.
type Counters struct {
	DiskRead, DiskWrite, NetRx, NetTx *uint64
}

// Rates turns each process's counters into rates between its observations.
type Rates struct {
	last map[string]observation
}

type observation struct {
	at       time.Time
	counters Counters
}

func NewRates() *Rates { return &Rates{last: map[string]observation{}} }

// Observe is each counter's rate since the process keyed key was last
// observed: nil on its first observation, and for a counter unread either
// time or that went backwards.
func (r *Rates) Observe(key string, at time.Time, counters Counters) PerSecond {
	previous, ok := r.last[key]
	r.last[key] = observation{at: at, counters: counters}
	elapsed := at.Sub(previous.at).Seconds()
	if !ok || elapsed <= 0 {
		return PerSecond{}
	}
	rate := func(before, now *uint64) *float64 {
		if before == nil || now == nil || *now < *before {
			return nil
		}
		perSecond := float64(*now-*before) / elapsed
		return &perSecond
	}
	return PerSecond{
		DiskRead:  rate(previous.counters.DiskRead, counters.DiskRead),
		DiskWrite: rate(previous.counters.DiskWrite, counters.DiskWrite),
		NetRx:     rate(previous.counters.NetRx, counters.NetRx),
		NetTx:     rate(previous.counters.NetTx, counters.NetTx),
	}
}

// Forget drops the processes whose keys are not alive.
func (r *Rates) Forget(alive map[string]bool) {
	for key := range r.last {
		if !alive[key] {
			delete(r.last, key)
		}
	}
}
