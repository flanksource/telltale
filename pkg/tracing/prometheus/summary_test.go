package prometheus

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"slices"
	"time"
)

var _ = Describe("scrape monitor summary", func() {
	It("counts latest target health instead of summing historical gauges", func() {
		rows := []Scrape{{Endpoint: "a", TakenAt: time.Unix(1, 0), Health: "down", Samples: 3}, {Endpoint: "a", TakenAt: time.Unix(2, 0), Health: "up", Samples: 7}, {Endpoint: "b", TakenAt: time.Unix(2, 0), Health: "down", Samples: 2}}
		summary, err := (kind{}).Summarize(slices.Values(rows))
		Expect(err).NotTo(HaveOccurred())
		Expect(summary).To(Equal(map[string]any{"targets": 2, "up": 1, "down": 1, "latestSamples": int64(9), "latestDurationMs": float64(0)}))
	})
})
