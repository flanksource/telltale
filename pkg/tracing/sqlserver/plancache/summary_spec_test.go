package plancache

import (
	"fmt"
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("plan cache summaries", func() {
	It("aggregates interval costs per plan and independently ranks CPU and elapsed", func() {
		rows := []Sample{
			{SampledAt: planCreated, PlanHandle: "cpu", Executions: 2, CPUMs: 9, ElapsedMs: 3, Recompiled: true},
			{SampledAt: planCreated.Add(time.Second), PlanHandle: "cpu", Executions: 3, CPUMs: 5, ElapsedMs: 7},
			{SampledAt: planCreated, PlanHandle: "elapsed", Executions: 1, CPUMs: 1, ElapsedMs: 30},
			{SampledAt: planCreated, PlanHandle: "gone", Evicted: true},
		}
		got, err := (handler{}).Summarize(slices.Values(rows))
		Expect(err).NotTo(HaveOccurred())
		Expect(got["polls"]).To(Equal(2))
		Expect(got["recompiled"]).To(Equal(1))
		Expect(got["evicted"]).To(Equal(1))
		Expect(got["topCPU"].([]Sample)[0]).To(And(HaveField("PlanHandle", "cpu"), HaveField("Executions", int64(5)), HaveField("CPUMs", float64(14))))
		Expect(got["topElapsed"].([]Sample)[0].PlanHandle).To(Equal("elapsed"))
	})
	It("keeps ten leaders and accepts an empty window", func() {
		var rows []Sample
		for n := range 12 {
			rows = append(rows, Sample{PlanHandle: fmt.Sprint(n), Executions: 1, CPUMs: float64(n), ElapsedMs: float64(12 - n)})
		}
		got, err := (handler{}).Summarize(slices.Values(rows))
		Expect(err).NotTo(HaveOccurred())
		Expect(got["topCPU"]).To(HaveLen(10))
		Expect(got["topElapsed"]).To(HaveLen(10))
		got, err = (handler{}).Summarize(slices.Values([]Sample{}))
		Expect(err).NotTo(HaveOccurred())
		Expect(got["polls"]).To(BeZero())
		Expect(got["topCPU"]).To(BeEmpty())
	})
})
