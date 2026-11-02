package process

import (
	"slices"

	"github.com/flanksource/telltale/pkg/tracing"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("process_samples summary", func() {
	It("skips missing CPU and computes each process's counter deltas and sample count", func() {
		cpuLow, cpuHigh := float64(10), float64(30)
		readLow, readHigh, writeLow, writeHigh := uint64(100), uint64(160), uint64(200), uint64(240)
		rows := []Sample{
			{PID: 10, Key: "10@1", RSSBytes: 100, DiskReadBytes: &readLow, DiskWriteBytes: &writeLow},
			{PID: 10, Key: "10@1", RSSBytes: 200, CPUPercent: &cpuLow},
			{PID: 10, Key: "10@1", RSSBytes: 300, CPUPercent: &cpuHigh, DiskReadBytes: &readHigh, DiskWriteBytes: &writeHigh},
			{PID: 20, Key: "20@1", RSSBytes: 50},
		}
		var summarizer tracing.Summarizer[Sample] = samples{}
		result, err := summarizer.Summarize(slices.Values(rows))
		Expect(err).NotTo(HaveOccurred())
		Expect(result["processes"]).To(ConsistOf(
			And(HaveKeyWithValue("pid", 10), HaveKeyWithValue("sampleCount", int64(3)), HaveKeyWithValue("cpuPercent", HaveKeyWithValue("avg", float64(20))), HaveKeyWithValue("rssBytes", HaveKeyWithValue("p95", float64(300))), HaveKeyWithValue("diskReadBytesDelta", uint64(60)), HaveKeyWithValue("diskWriteBytesDelta", uint64(40))),
			And(HaveKeyWithValue("pid", 20), HaveKeyWithValue("sampleCount", int64(1)), HaveKeyWithValue("cpuPercent", HaveKeyWithValue("count", int64(0))), HaveKeyWithValue("diskReadBytesDelta", BeNil())),
		))
	})
})
