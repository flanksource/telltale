package plancache

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var planCreated = time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC)

func testPlan() CachedPlan {
	return CachedPlan{PlanHandle: "0x01", QueryHash: "0xAA", QueryPlanHash: "0xBB",
		Database: "warehouse", Object: "report", ObjType: "Proc", SQL: "SELECT 1",
		CreationTime: planCreated, ExecutionCount: 10, TotalCPU: 20 * time.Millisecond,
		TotalElapsed: 40 * time.Millisecond, TotalLogicalReads: 100, TotalPhysicalReads: 20,
		TotalWrites: 10, TotalRows: 200, UseCounts: 5, SizeBytes: 4096}
}

var _ = Describe("Delta", func() {
	It("computes interval counters and fractional millisecond averages", func() {
		prev := testPlan()
		cur := prev
		cur.ExecutionCount += 2
		cur.TotalCPU += 3500 * time.Microsecond
		cur.TotalElapsed += 7 * time.Millisecond
		cur.TotalLogicalReads += 30
		cur.TotalPhysicalReads += 3
		cur.TotalWrites += 4
		cur.TotalRows += 50
		got, emit, err := Delta(prev, cur)
		Expect(err).NotTo(HaveOccurred())
		Expect(emit).To(BeTrue())
		Expect(got).To(Equal(Sample{PlanHandle: cur.PlanHandle, QueryHash: cur.QueryHash,
			QueryPlanHash: cur.QueryPlanHash, Database: cur.Database, Object: cur.Object,
			ObjType: cur.ObjType, SQL: cur.SQL, CreationTime: cur.CreationTime,
			Executions: 2, CPUMs: 3.5, ElapsedMs: 7, LogicalReads: 30, PhysicalReads: 3,
			Writes: 4, Rows: 50, AvgCPUMs: 1.75, AvgElapsedMs: 3.5,
			TotalExecutions: 12, TotalCPUMs: 23.5, TotalElapsedMs: 47,
			UseCounts: 5, SizeBytes: 4096}))
	})
	DescribeTable("handles lifecycle changes", func(prev CachedPlan, cur CachedPlan, emit, recompiled bool) {
		got, ok, err := Delta(prev, cur)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(Equal(emit))
		Expect(got.Recompiled).To(Equal(recompiled))
		if emit {
			Expect(got.Executions).To(Equal(cur.ExecutionCount))
		}
	},
		Entry("unchanged counters", testPlan(), testPlan(), false, false),
		Entry("new plan", CachedPlan{}, testPlan(), true, false),
		Entry("recompiled plan", func() CachedPlan {
			p := testPlan()
			p.CreationTime = planCreated.Add(-time.Hour)
			p.ExecutionCount = 100
			return p
		}(), testPlan(), true, true),
		Entry("new plan with no executions", CachedPlan{}, CachedPlan{PlanHandle: "0x01"}, false, false),
	)
	DescribeTable("rejects decreasing counters", func(decrease func(*CachedPlan)) {
		prev := testPlan()
		cur := prev
		cur.ExecutionCount++
		decrease(&cur)
		_, emit, err := Delta(prev, cur)
		Expect(err).To(HaveOccurred())
		Expect(emit).To(BeFalse())
	},
		Entry("executions", func(p *CachedPlan) { p.ExecutionCount = 9 }),
		Entry("cpu", func(p *CachedPlan) { p.TotalCPU-- }),
		Entry("elapsed", func(p *CachedPlan) { p.TotalElapsed-- }),
		Entry("logical reads", func(p *CachedPlan) { p.TotalLogicalReads-- }),
		Entry("physical reads", func(p *CachedPlan) { p.TotalPhysicalReads-- }),
		Entry("writes", func(p *CachedPlan) { p.TotalWrites-- }),
		Entry("rows", func(p *CachedPlan) { p.TotalRows-- }),
		Entry("negative current total", func(p *CachedPlan) { p.TotalRows = -1 }),
	)
	It("rejects invalid plan identity", func() {
		cur := testPlan()
		cur.PlanHandle = "0x02"
		_, _, err := Delta(testPlan(), cur)
		Expect(err).To(HaveOccurred())
		_, _, err = Delta(CachedPlan{}, CachedPlan{})
		Expect(err).To(HaveOccurred())
	})
})
