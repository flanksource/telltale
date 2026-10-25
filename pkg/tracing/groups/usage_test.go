package groups_test

import (
	"time"

	"github.com/flanksource/telltale/pkg/tracing/groups"
	"github.com/flanksource/telltale/pkg/tracing/process"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func float(value float64) *float64 { return &value }
func count(value int) *int         { return &value }
func bytes(value uint64) *uint64   { return &value }

var _ = Describe("Aggregate", func() {
	It("sums a group's own processes as self and adds its descendant groups into total", func() {
		classified := []groups.Group{
			{Key: "10", Members: []process.Observed{observed(10, 1, "claude"), observed(11, 10, "zsh")}},
			{Key: "20", ParentKey: "10", Members: []process.Observed{observed(20, 11, "gavel")}},
			{Key: "30", ParentKey: "20", Members: []process.Observed{observed(30, 20, "go test")}},
		}
		readings := map[string]groups.Reading{
			"10": {CPUPercent: float(5), RSSBytes: 100, Sockets: count(2), PerSecond: groups.PerSecond{NetRx: float(1000)}},
			"11": {RSSBytes: 10},
			"20": {CPUPercent: float(20), RSSBytes: 50, PerSecond: groups.PerSecond{DiskWrite: float(300)}},
			"30": {CPUPercent: float(75), RSSBytes: 400, Connections: count(1)},
		}

		Expect(groups.Aggregate(classified, readings)).To(Equal(map[string]groups.Totals{
			"10": {
				Self:  groups.Usage{Processes: 2, CPUPercent: float(5), RSSBytes: 110, Sockets: count(2), PerSecond: groups.PerSecond{NetRx: float(1000)}},
				Total: groups.Usage{Processes: 4, CPUPercent: float(100), RSSBytes: 560, Sockets: count(2), Connections: count(1), PerSecond: groups.PerSecond{NetRx: float(1000), DiskWrite: float(300)}},
			},
			"20": {
				Self:  groups.Usage{Processes: 1, CPUPercent: float(20), RSSBytes: 50, PerSecond: groups.PerSecond{DiskWrite: float(300)}},
				Total: groups.Usage{Processes: 2, CPUPercent: float(95), RSSBytes: 450, Connections: count(1), PerSecond: groups.PerSecond{DiskWrite: float(300)}},
			},
			"30": {
				Self:  groups.Usage{Processes: 1, CPUPercent: float(75), RSSBytes: 400, Connections: count(1)},
				Total: groups.Usage{Processes: 1, CPUPercent: float(75), RSSBytes: 400, Connections: count(1)},
			},
		}))
	})
})

var _ = Describe("Rates", func() {
	start := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

	It("rates each counter over the time between two observations of one process", func() {
		rates := groups.NewRates()
		first := rates.Observe("7@100", start, groups.Counters{DiskRead: bytes(1000), NetTx: bytes(0)})
		second := rates.Observe("7@100", start.Add(2*time.Second), groups.Counters{DiskRead: bytes(5000), NetTx: bytes(600)})

		Expect(first).To(Equal(groups.PerSecond{}), "a first observation has nothing to rate against")
		Expect(second).To(Equal(groups.PerSecond{DiskRead: float(2000), NetTx: float(300)}))
	})

	It("does not rate a counter that went backwards, starting over from it", func() {
		rates := groups.NewRates()
		rates.Observe("7@100", start, groups.Counters{DiskRead: bytes(5000)})
		backwards := rates.Observe("7@100", start.Add(time.Second), groups.Counters{DiskRead: bytes(10)})
		after := rates.Observe("7@100", start.Add(2*time.Second), groups.Counters{DiskRead: bytes(110)})

		Expect(backwards).To(Equal(groups.PerSecond{}))
		Expect(after).To(Equal(groups.PerSecond{DiskRead: float(100)}))
	})

	It("forgets a process that is no longer listed, so a reused key starts over", func() {
		rates := groups.NewRates()
		rates.Observe("7@100", start, groups.Counters{DiskRead: bytes(1000)})
		rates.Forget(map[string]bool{})

		Expect(rates.Observe("7@100", start.Add(time.Second), groups.Counters{DiskRead: bytes(9000)})).To(Equal(groups.PerSecond{}))
	})
})
