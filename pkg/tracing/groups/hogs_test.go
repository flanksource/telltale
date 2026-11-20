package groups

import (
	"time"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("resource hogs", func() {
	start := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	host := HostUsage{Cores: 8, MemoryTotalBytes: 16 << 30, SwapTotalBytes: 4 << 30}
	ginkgo.DescribeTable("detects excessive use of each resource", func(reading Reading, resource string) {
		readings := map[string]Reading{"worker@1": reading}
		var tracker hogTracker
		tracker.observe(readings, host, start)
		Expect(readings["worker@1"].Hogs).To(Equal(Hogs{resource: start.Add(HogRetention)}))
	},
		ginkgo.Entry("CPU across all cores", Reading{CPUPercent: new(201.0)}, "CPU"),
		ginkgo.Entry("resident memory", Reading{RSSBytes: 5 << 30}, "memory"),
		ginkgo.Entry("swap", Reading{SwapBytes: new(uint64(2 << 30))}, "swap"),
		ginkgo.Entry("compressed memory", Reading{CompressedBytes: new(uint64(5 << 30))}, "compressed"),
	)

	ginkgo.It("uses strict thresholds and never flags unavailable capacities", func() {
		readings := map[string]Reading{"worker@1": {CPUPercent: new(200.0), RSSBytes: 4 << 30, SwapBytes: new(uint64(1 << 30)), CompressedBytes: new(uint64(4 << 30))}}
		var tracker hogTracker
		tracker.observe(readings, host, start)
		Expect(readings["worker@1"].Hogs).To(BeEmpty())
		tracker.observe(readings, HostUsage{}, start)
		Expect(readings["worker@1"].Hogs).To(BeEmpty())
	})

	ginkgo.It("retains a spike for exactly 120 seconds and extends only the resource still hogging", func() {
		var tracker hogTracker
		readings := map[string]Reading{"worker@1": {CPUPercent: new(201.0), RSSBytes: 5 << 30}}
		tracker.observe(readings, host, start)
		first := readings["worker@1"].Hogs
		readings["worker@1"] = Reading{RSSBytes: 5 << 30}
		tracker.observe(readings, host, start.Add(time.Minute))
		Expect(first).To(Equal(Hogs{"CPU": start.Add(HogRetention), "memory": start.Add(HogRetention)}), "previous frames must not mutate")
		readings["worker@1"] = Reading{}
		tracker.observe(readings, host, start.Add(HogRetention))
		Expect(readings["worker@1"].Hogs).To(Equal(Hogs{"memory": start.Add(3 * time.Minute)}))
		Expect(readings["worker@1"].Hogs.Pinned(start.Add(3*time.Minute - time.Nanosecond))).To(BeTrue())
		tracker.observe(readings, host, start.Add(3*time.Minute))
		Expect(readings["worker@1"].Hogs.Pinned(start.Add(3 * time.Minute))).To(BeFalse())
	})

	ginkgo.It("forgets exited processes and does not transfer pins to a reused pid", func() {
		var tracker hogTracker
		readings := map[string]Reading{"100@1": {RSSBytes: 5 << 30}}
		tracker.observe(readings, host, start)
		readings = map[string]Reading{"100@2": {}}
		tracker.observe(readings, host, start.Add(time.Second))
		Expect(readings["100@2"].Hogs).To(BeEmpty())
		Expect(tracker.last).NotTo(HaveKey("100@1"))
	})

	ginkgo.It("rolls up swap, compressed memory and descendant pins without double counting", func() {
		hogs := Hogs{"swap": start.Add(HogRetention)}
		groups := Rollup([]GroupSample{
			{Key: "parent", Self: Usage{Processes: 1, RSSBytes: 100, SwapBytes: new(uint64(10)), CompressedBytes: new(uint64(20))}},
			{Key: "child", ParentKey: "parent", Self: Usage{Processes: 1, RSSBytes: 200, SwapBytes: new(uint64(30)), CompressedBytes: new(uint64(40)), Hogs: hogs}},
		})
		Expect(groups[0].Total).To(Equal(Usage{Processes: 2, RSSBytes: 300, SwapBytes: new(uint64(40)), CompressedBytes: new(uint64(60)), Hogs: hogs}))
		Expect(groups[0].Self.Hogs).To(BeNil())
	})

	ginkgo.It("pins the branch containing a hog ahead of every sort direction", func() {
		frame := Frame{SampledAt: start, Groups: []GroupSample{
			{Key: "busy", Name: "a", Total: Usage{CPUPercent: new(100.0), RSSBytes: 1000}},
			{Key: "hog", Name: "z", Total: Usage{Hogs: Hogs{"memory": start.Add(HogRetention)}}},
		}}
		for _, by := range Sorts {
			for _, reverse := range []bool{false, true} {
				sorted, err := frame.Sorted(Order{By: by, Reverse: reverse})
				Expect(err).NotTo(HaveOccurred())
				Expect(sorted.Groups[0].Key).To(Equal("hog"), "%s reverse=%t", by, reverse)
			}
		}
		frame.SampledAt = start.Add(HogRetention)
		sorted, err := frame.Sorted(Order{By: SortMemory})
		Expect(err).NotTo(HaveOccurred())
		Expect(sorted.Groups[0].Key).To(Equal("busy"))
	})
})
