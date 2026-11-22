package server

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the Top CPU budget", func() {
	It("paces polls according to measured CPU time, including the first poll", func() {
		Expect(topPollInterval(2 * time.Millisecond)).To(Equal(time.Second))
		Expect(topPollInterval(135 * time.Millisecond)).To(Equal(15 * time.Second))
		Expect(topPollInterval(time.Second)).To(Equal(time.Minute))
	})

	It("reserves CPU from the same rolling minute for all views", func() {
		var budget topBudget
		start := time.Now()
		budget.record(start, 300*time.Millisecond)
		budget.record(start.Add(10*time.Second), 200*time.Millisecond)

		at := start.Add(30 * time.Second)
		Expect(budget.wait(at, 100*time.Millisecond)).To(Equal(30 * time.Second))
		Expect(budget.cpuPercent()).To(BeNumerically("~", 0.83333, 0.00001))

		at = start.Add(time.Minute)
		Expect(budget.wait(at, 100*time.Millisecond)).To(BeZero())
		Expect(budget.cpuPercent()).To(BeNumerically("~", 0.33333, 0.00001))
	})

	It("waits for a costly poll to age out before allowing another", func() {
		var budget topBudget
		start := time.Now()
		budget.record(start, 700*time.Millisecond)

		Expect(budget.wait(start.Add(time.Second), 700*time.Millisecond)).To(Equal(59 * time.Second))
		Expect(budget.wait(start.Add(time.Minute), 700*time.Millisecond)).To(BeZero())
	})
})
