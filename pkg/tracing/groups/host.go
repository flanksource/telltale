package groups

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
)

// HostUsage is what the whole host uses at a tick: its CPU split between
// processes (user) and the kernel (system) over the time since the tick
// before, nil on the first, and its memory and load.
type HostUsage struct {
	Cores            int      `json:"cores"`
	CPUUserPercent   *float64 `json:"cpuUserPercent,omitempty"`
	CPUSystemPercent *float64 `json:"cpuSystemPercent,omitempty"`
	CPUIdlePercent   *float64 `json:"cpuIdlePercent,omitempty"`
	MemoryUsedBytes  uint64   `json:"memoryUsedBytes"`
	MemoryTotalBytes uint64   `json:"memoryTotalBytes"`
	SwapUsedBytes    uint64   `json:"swapUsedBytes"`
	SwapTotalBytes   uint64   `json:"swapTotalBytes"`
	Load1            float64  `json:"load1"`
	Load5            float64  `json:"load5"`
	Load15           float64  `json:"load15"`
	DiskUsage
}

// DiskUsage is what the host's whole disks did per second since the tick
// before: the operations they served and the bytes they moved; nil on the
// first tick.
type DiskUsage struct {
	ReadOps  *float64 `json:"diskReadOps,omitempty"`
	WriteOps *float64 `json:"diskWriteOps,omitempty"`
	ReadBps  *float64 `json:"diskReadBps,omitempty"`
	WriteBps *float64 `json:"diskWriteBps,omitempty"`
}

// DiskRates are the operations and bytes per second of the whole disks
// both readings hold, over seconds between them: a partition counts nothing
// more than its disk, a loop or ram device nothing, nor a disk whose
// counters went backwards. Over no time nothing is rated.
func DiskRates(before, after map[string]disk.IOCountersStat, seconds float64) DiskUsage {
	if seconds <= 0 {
		return DiskUsage{}
	}
	var readOps, writeOps, readBytes, writeBytes float64
	for name, now := range after {
		then, ok := before[name]
		if !ok || !wholeDisk(name, after) || now.ReadCount < then.ReadCount || now.WriteCount < then.WriteCount ||
			now.ReadBytes < then.ReadBytes || now.WriteBytes < then.WriteBytes {
			continue
		}
		readOps += float64(now.ReadCount - then.ReadCount)
		writeOps += float64(now.WriteCount - then.WriteCount)
		readBytes += float64(now.ReadBytes - then.ReadBytes)
		writeBytes += float64(now.WriteBytes - then.WriteBytes)
	}
	perSecond := func(total float64) *float64 { rate := total / seconds; return &rate }
	return DiskUsage{ReadOps: perSecond(readOps), WriteOps: perSecond(writeOps), ReadBps: perSecond(readBytes), WriteBps: perSecond(writeBytes)}
}

// wholeDisk reports a device that is no partition of another one listed,
// such as linux's sda1 of sda, and no loop or ram device.
func wholeDisk(name string, devices map[string]disk.IOCountersStat) bool {
	if strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") {
		return false
	}
	for other := range devices {
		if other != name && strings.HasPrefix(name, other) {
			return false
		}
	}
	return true
}

// CPUShares are the shares, in percent of every core, of the time between
// two readings of the host's CPU times spent running processes, in the
// kernel, and idle; false when no time passed between them.
func CPUShares(before, after cpu.TimesStat) (user, system, idle float64, ok bool) {
	userTime := (after.User + after.Nice) - (before.User + before.Nice)
	systemTime := (after.System + after.Irq + after.Softirq) - (before.System + before.Irq + before.Softirq)
	idleTime := (after.Idle + after.Iowait) - (before.Idle + before.Iowait)
	total := userTime + systemTime + idleTime + (after.Steal - before.Steal)
	if total <= 0 {
		return 0, 0, 0, false
	}
	return 100 * userTime / total, 100 * systemTime / total, 100 * idleTime / total, true
}

// hostReader reads the host's use, keeping the CPU times its next reading
// rates against.
type hostReader struct {
	last    *cpu.TimesStat
	disks   map[string]disk.IOCountersStat
	disksAt time.Time
}

func (h *hostReader) read(ctx context.Context) (HostUsage, error) {
	times, err := cpu.TimesWithContext(ctx, false)
	if err != nil || len(times) != 1 {
		return HostUsage{}, fmt.Errorf("read host cpu times: %w (%d totals)", err, len(times))
	}
	cores, err := cpu.CountsWithContext(ctx, true)
	if err != nil {
		return HostUsage{}, fmt.Errorf("count host cpus: %w", err)
	}
	memory, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return HostUsage{}, fmt.Errorf("read host memory: %w", err)
	}
	swap, err := mem.SwapMemoryWithContext(ctx)
	if err != nil {
		return HostUsage{}, fmt.Errorf("read host swap: %w", err)
	}
	average, err := load.AvgWithContext(ctx)
	if err != nil {
		return HostUsage{}, fmt.Errorf("read host load: %w", err)
	}
	usage := HostUsage{
		Cores: cores, MemoryUsedBytes: memory.Used, MemoryTotalBytes: memory.Total,
		SwapUsedBytes: swap.Used, SwapTotalBytes: swap.Total, Load1: average.Load1, Load5: average.Load5, Load15: average.Load15,
	}
	if h.last != nil {
		if user, system, idle, ok := CPUShares(*h.last, times[0]); ok {
			usage.CPUUserPercent, usage.CPUSystemPercent, usage.CPUIdlePercent = &user, &system, &idle
		}
	}
	h.last = &times[0]
	disks, err := disk.IOCountersWithContext(ctx)
	if err != nil {
		return HostUsage{}, fmt.Errorf("read host disk counters: %w", err)
	}
	now := time.Now()
	if h.disks != nil {
		usage.DiskUsage = DiskRates(h.disks, disks, now.Sub(h.disksAt).Seconds())
	}
	h.disks, h.disksAt = disks, now
	return usage, nil
}
