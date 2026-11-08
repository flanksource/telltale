package traces

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("run hostname prefix", func() {
	DescribeTable("uses up to nine dash-free characters of the short hostname", func(hostname, expected string) {
		prefix, err := hostnamePrefix(hostname)
		Expect(err).NotTo(HaveOccurred())
		Expect(prefix).To(Equal(expected))
	},
		Entry("short", "dev", "dev"),
		Entry("long", "buildworker12", "buildwork"),
		Entry("qualified", "worker.example.com", "worker"),
		Entry("dashes", "build-worker-12.local", "buildwork"),
	)
	DescribeTable("refuses unusable hostnames", func(hostname string) {
		_, err := hostnamePrefix(hostname)
		Expect(err).To(HaveOccurred())
	}, Entry("empty", ""), Entry("dashes alone", "---"), Entry("path", "../escape"))
})
