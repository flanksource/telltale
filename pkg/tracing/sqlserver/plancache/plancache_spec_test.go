package plancache

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("cached plan conversion", func() {
	It("converts microseconds and derives averages from executions", func() {
		cpu, elapsed, executions, reads := int64(2_500_000), int64(4_000_000), int64(4), int64(1000)
		got := (cachedPlanRow{TotalWorkerMicros: &cpu, TotalElapsedMicros: &elapsed,
			ExecutionCount: &executions, TotalLogicalReads: &reads}).toCachedPlan()
		Expect(got).To(Equal(CachedPlan{ExecutionCount: executions, TotalCPU: 2500 * time.Millisecond,
			AvgCPU: 625 * time.Millisecond, TotalElapsed: 4 * time.Second, AvgElapsed: time.Second,
			TotalLogicalReads: reads, AvgLogicalReads: 250}))
	})
	It("retains cache metadata when query statistics and text are NULL", func() {
		got := (cachedPlanRow{ObjType: "Adhoc", CacheObjType: "Compiled Plan Stub", UseCounts: 7}).toCachedPlan()
		Expect(got).To(Equal(CachedPlan{ObjType: "Adhoc", CacheObjType: "Compiled Plan Stub", UseCounts: 7}))
	})
	It("preserves binary identities, text and timestamps", func() {
		database, object, statement := "warehouse", "report", "SELECT 1"
		got := (cachedPlanRow{PlanHandle: []byte{1}, QueryHash: []byte{0xaa}, QueryPlanHash: []byte{0xbb},
			Database: &database, Object: &object, SQL: &statement,
			CreationTime: &planCreated, LastExecutionTime: &planCreated}).toCachedPlan()
		Expect(got).To(Equal(CachedPlan{PlanHandle: "0x01", QueryHash: "0xAA", QueryPlanHash: "0xBB",
			Database: database, Object: object, SQL: statement, CreationTime: planCreated, LastExecutionTime: planCreated}))
	})
	DescribeTable("decodes SQL Server hex handles", func(encoded string) {
		decoded, err := DecodeHandle(encoded)
		Expect(err).NotTo(HaveOccurred())
		Expect(decoded).To(Equal([]byte{0x06, 0x00, 0xab}))
		Expect(EncodeHandle(decoded)).To(Equal("0x0600AB"))
	},
		Entry("prefixed", "0x0600AB"),
		Entry("unprefixed", "0600ab"),
		Entry("uppercase prefix and whitespace", " 0X0600ab "),
	)
	It("represents an absent binary handle as empty text", func() {
		Expect(EncodeHandle(nil)).To(BeEmpty())
	})
	DescribeTable("rejects malformed handles", func(value string) {
		_, err := DecodeHandle(value)
		Expect(err).To(HaveOccurred())
	},
		Entry("empty", ""),
		Entry("empty prefix", "0x"),
		Entry("whitespace", "  "),
		Entry("nonhex", "0xnothex"),
		Entry("odd number of digits", "0x123"),
	)
})
