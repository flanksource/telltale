package deadlock

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("deadlock window params", func() {
	It("binds relative bounds to the same instant", func() {
		window, err := (Options{From: "now-2h", To: "now-1h"}).window()
		Expect(err).NotTo(HaveOccurred())
		Expect(window.To.Sub(window.From)).To(Equal(time.Hour))
	})

	DescribeTable("refuses invalid input", func(p Options) { Expect(p.Validate()).To(HaveOccurred()) },
		Entry("missing connection", Options{}),
		Entry("end without start", Options{Connection: "connection://example", To: "2026-10-28T12:00:00Z"}),
		Entry("reversed window", Options{Connection: "connection://example", From: "2026-10-28T12:00:00Z", To: "2026-10-28T11:00:00Z"}),
		Entry("negative polling", Options{Connection: "connection://example", Poll: "-1s"}),
	)
})
