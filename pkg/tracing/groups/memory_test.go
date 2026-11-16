package groups

import (
	"strings"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("compressed memory listing", func() {
	ginkgo.It("reads macOS top's compressed bytes independently of resident memory and swap", func() {
		listing := "Processes: 3 total\nPhysMem: 16G used\n\nPID CMPRS\n100 0B\n200 1.5G\n300 8384K\n0 0B\n"
		values, err := parseCompressed(strings.NewReader(listing))
		Expect(err).NotTo(HaveOccurred())
		Expect(values).To(Equal(map[int]*uint64{100: new(uint64(0)), 200: new(uint64(1536 << 20)), 300: new(uint64(8384 << 10))}))
	})

	ginkgo.DescribeTable("refuses malformed listings", func(listing string) {
		_, err := parseCompressed(strings.NewReader(listing))
		Expect(err).To(HaveOccurred())
	},
		ginkgo.Entry("no header", "100 20M\n"),
		ginkgo.Entry("no processes", "PID CMPRS\n"),
		ginkgo.Entry("invalid units", "PID CMPRS\n100 20X\n"),
		ginkgo.Entry("invalid pid", "PID CMPRS\nworker 20M\n"),
		ginkgo.Entry("missing column", "PID CMPRS\n100\n"),
		ginkgo.Entry("negative memory", "PID CMPRS\n100 -1M\n"),
		ginkgo.Entry("non-finite memory", "PID CMPRS\n100 NaNG\n"),
		ginkgo.Entry("overflow", "PID CMPRS\n100 999999999999999999G\n"),
	)
})
