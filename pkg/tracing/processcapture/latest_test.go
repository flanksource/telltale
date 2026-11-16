package processcapture

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("latest process header", func() {
	It("retains each identity and its last sample including exits and reused PIDs", func() {
		at := time.Unix(100, 0).UTC()
		exited := at.Add(time.Second)
		cpu := 125.0
		rows, err := LatestProcesses([]Identity{
			{Key: "7@new", PID: 7, Name: "new worker"},
			{Key: "7@old", PID: 7, Name: "old worker", ExitedAt: &exited},
		}, []MetricRow{
			{Entity: "7@old", At: at, Tick: 1, RSSBytes: 1024, MemoryPercent: 2},
			{Entity: "7@old", At: exited, Tick: 2, CPUPercent: &cpu, RSSBytes: 2048, MemoryPercent: 4},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(ConsistOf(
			And(HaveField("Key", "7@old"), HaveField("ExitedAt", Equal(&exited)), HaveField("CPUPercent", Equal(&cpu)), HaveField("RSSBytes", HaveValue(Equal(uint64(2048))))),
			And(HaveField("Key", "7@new"), HaveField("CPUPercent", BeNil()), HaveField("RSSBytes", BeNil())),
		))
	})
	It("rejects measurements without their recorded identity", func() {
		_, err := LatestProcesses(nil, []MetricRow{{Entity: "missing"}})
		Expect(err).To(MatchError(ContainSubstring("unknown process")))
	})
})
