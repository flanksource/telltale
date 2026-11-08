package groups

import (
	"maps"
	"strings"
	"time"
)

// Hogs keeps each resource's pin expiry, measured from its last excessive sample.
type Hogs map[string]time.Time

const HogRetention = 120 * time.Second

func (h Hogs) Pinned(at time.Time) bool {
	for _, until := range h {
		if until.After(at) {
			return true
		}
	}
	return false
}

func (h Hogs) Label(at time.Time) string {
	var resources []string
	for _, resource := range []string{"CPU", "memory", "swap", "compressed"} {
		if h[resource].After(at) {
			resources = append(resources, resource)
		}
	}
	if len(resources) == 0 {
		return ""
	}
	return strings.Join(resources, "/") + " hog"
}

func (h Hogs) merge(other Hogs) Hogs {
	merged := maps.Clone(h)
	for resource, until := range other {
		if until.After(merged[resource]) {
			if merged == nil {
				merged = Hogs{}
			}
			merged[resource] = until
		}
	}
	return merged
}

type hogTracker struct {
	last map[string]Hogs
}

func (h *hogTracker) observe(readings map[string]Reading, host HostUsage, at time.Time) {
	next := make(map[string]Hogs, len(readings))
	for key, reading := range readings {
		var recent Hogs
		for resource, until := range h.last[key] {
			if until.After(at) {
				if recent == nil {
					recent = Hogs{}
				}
				recent[resource] = until
			}
		}
		for resource, hogging := range map[string]bool{
			"CPU":        host.Cores > 0 && reading.CPUPercent != nil && *reading.CPUPercent > 25*float64(host.Cores),
			"memory":     host.MemoryTotalBytes > 0 && reading.RSSBytes > host.MemoryTotalBytes/4,
			"swap":       host.SwapTotalBytes > 0 && reading.SwapBytes != nil && *reading.SwapBytes > host.SwapTotalBytes/4,
			"compressed": host.MemoryTotalBytes > 0 && reading.CompressedBytes != nil && *reading.CompressedBytes > host.MemoryTotalBytes/4,
		} {
			if hogging {
				if recent == nil {
					recent = Hogs{}
				}
				recent[resource] = at.Add(HogRetention)
			}
		}
		reading.Hogs = recent
		readings[key], next[key] = reading, recent
	}
	h.last = next
}
