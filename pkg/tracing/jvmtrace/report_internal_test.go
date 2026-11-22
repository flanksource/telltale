package jvmtrace

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("method trace reports", func() {
	It("sums losses and reports restored only when every armed session confirms it", func() {
		first := map[string]any{"instrumented": true, "restored": true, "probeDropped": float64(2), "filtered": float64(1)}
		second := map[string]any{"instrumented": true, "restored": false, "probeDropped": float64(3), "filtered": float64(4)}
		Expect(summarizeMethodReports([]map[string]any{first, second, nil})).To(Equal(map[string]any{
			"instrumented": true, "restored": false, "probeDropped": float64(5), "filtered": float64(5),
		}))
		second["restored"] = true
		Expect(summarizeMethodReports([]map[string]any{first, second})).To(HaveKeyWithValue("restored", true))
	})
})
