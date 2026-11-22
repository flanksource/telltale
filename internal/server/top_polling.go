package server

import "time"

const (
	topCPUWindow    = time.Minute
	topCPUAllowance = 540 * time.Millisecond // 0.9% of one core, with headroom for cost changes.
)

// TopPolling tells clients how long to wait before polling again.
type TopPolling struct {
	MinimumIntervalMS int64 `json:"minimumIntervalMs"`
}

type topCharge struct {
	at  time.Time
	cpu time.Duration
}

type topBudget struct {
	recent []topCharge
	used   time.Duration
}

func (b *topBudget) record(at time.Time, cpu time.Duration) {
	b.recent = append(b.recent, topCharge{at: at, cpu: cpu})
	b.used += cpu
}

// wait computes when the next estimated charge fits in the rolling window.
func (b *topBudget) wait(now time.Time, estimate time.Duration) time.Duration {
	for len(b.recent) > 0 && !b.recent[0].at.Add(topCPUWindow).After(now) {
		b.used -= b.recent[0].cpu
		b.recent = b.recent[1:]
	}
	estimate = min(estimate, topCPUAllowance)
	if b.used+estimate <= topCPUAllowance {
		return 0
	}
	used := b.used
	for _, charge := range b.recent {
		used -= charge.cpu
		if used+estimate <= topCPUAllowance {
			return charge.at.Add(topCPUWindow).Sub(now)
		}
	}
	return 0
}

func (b *topBudget) cpuPercent() float64 {
	return 100 * float64(b.used) / float64(topCPUWindow)
}

func topPollInterval(cost time.Duration) time.Duration {
	cost = min(cost, topCPUAllowance)
	return max(time.Second, cost*(topCPUWindow/time.Millisecond)/(topCPUAllowance/time.Millisecond))
}
