package groups_test

import (
	"github.com/flanksource/telltale/pkg/tracing/groups"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
)

var _ = Describe("DiskRates", func() {
	before := map[string]disk.IOCountersStat{
		"disk0":   {Name: "disk0", ReadCount: 100, WriteCount: 50, ReadBytes: 1 << 20, WriteBytes: 2 << 20},
		"disk4":   {Name: "disk4", ReadCount: 10, WriteCount: 10, ReadBytes: 0, WriteBytes: 0},
		"nvme0n1": {Name: "nvme0n1", ReadCount: 1000, WriteCount: 0, ReadBytes: 0, WriteBytes: 0},
	}

	It("rates the operations and bytes of every whole disk over the time between two readings", func() {
		after := map[string]disk.IOCountersStat{
			"disk0":     {Name: "disk0", ReadCount: 300, WriteCount: 150, ReadBytes: 5 << 20, WriteBytes: 2 << 20},
			"disk4":     {Name: "disk4", ReadCount: 30, WriteCount: 10, ReadBytes: 1 << 20, WriteBytes: 0},
			"nvme0n1":   {Name: "nvme0n1", ReadCount: 1200, WriteCount: 0, ReadBytes: 0, WriteBytes: 0},
			"nvme0n1p1": {Name: "nvme0n1p1", ReadCount: 999999, WriteCount: 999999, ReadBytes: 1 << 30, WriteBytes: 1 << 30},
			"loop0":     {Name: "loop0", ReadCount: 999999},
		}

		Expect(groups.DiskRates(before, after, 2)).To(Equal(groups.DiskUsage{
			ReadOps: float(210), WriteOps: float(50), ReadBps: float(5 << 19), WriteBps: float(0),
		}), "a partition and a loop device count nothing more than their disks; a disk new since the first counts from the next")
	})

	It("does not rate a disk whose counters went backwards, and rates nothing over no time", func() {
		after := map[string]disk.IOCountersStat{"disk0": {Name: "disk0", ReadCount: 1, WriteCount: 1}}

		Expect(groups.DiskRates(before, after, 2)).To(Equal(groups.DiskUsage{ReadOps: float(0), WriteOps: float(0), ReadBps: float(0), WriteBps: float(0)}))
		Expect(groups.DiskRates(before, before, 0)).To(Equal(groups.DiskUsage{}))
	})
})

var _ = Describe("CPUShares", func() {
	It("splits the time between two readings into processes', the kernel's and idle, in percent of every core", func() {
		before := cpu.TimesStat{User: 100, Nice: 10, System: 50, Irq: 5, Idle: 800, Iowait: 20}
		after := cpu.TimesStat{User: 130, Nice: 10, System: 60, Irq: 5, Idle: 850, Iowait: 30}

		user, system, idle, ok := groups.CPUShares(before, after)

		Expect([]any{user, system, idle, ok}).To(Equal([]any{30.0, 10.0, 60.0, true}))
	})

	It("rates nothing between two readings of the same moment", func() {
		reading := cpu.TimesStat{User: 100, Idle: 800}

		_, _, _, ok := groups.CPUShares(reading, reading)

		Expect(ok).To(BeFalse())
	})
})
