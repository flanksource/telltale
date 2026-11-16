package exec

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"slices"
)

var _ = Describe("execution monitor summary", func() {
	It("counts errors and duration across more than one displayed page", func() {
		rows := make([]ExecRecord, 125)
		for i := range rows {
			rows[i].DurationMs = 2
		}
		rows[124].ExitCode = 1
		summary, err := (capture{}).Summarize(slices.Values(rows))
		Expect(err).NotTo(HaveOccurred())
		Expect(summary).To(Equal(map[string]any{"executions": 125, "failures": 1, "durationMs": float64(250)}))
	})
})
