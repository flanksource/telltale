package deadlock

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("deadlock window params", func() {
	DescribeTable("refuses invalid input", func(p Params) { Expect(p.Validate()).To(HaveOccurred()) },
		Entry("missing connection", Params{}),
		Entry("end without start", Params{Connection: "connection://example", To: "2026-10-28T12:00:00Z"}),
		Entry("reversed window", Params{Connection: "connection://example", From: "2026-10-28T12:00:00Z", To: "2026-10-28T11:00:00Z"}),
		Entry("negative polling", Params{Connection: "connection://example", Poll: "-1s"}),
	)
})
