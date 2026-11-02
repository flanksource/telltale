package target_test

import (
	"github.com/flanksource/telltale/pkg/tracing/target"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("connection targets", func() {
	It("resolves without a local process and refuses a process selector", func() {
		seam := target.Seam("connection")
		selected, err := target.Resolve("sql_xevent", target.Target{}, []target.Seam{seam}, seam, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(selected.Seam).To(Equal(seam))
		_, err = target.Resolve("sql_xevent", target.Target{PID: 123}, []target.Seam{seam}, seam, nil)
		Expect(err).To(HaveOccurred())
	})
})
