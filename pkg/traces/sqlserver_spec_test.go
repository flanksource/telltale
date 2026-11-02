package traces_test

import (
	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/tracing/target"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("SQL Server builtins", func() {
	It("offers XEvents through the connection seam", func() {
		kind, err := traces.Registered("sql_xevent")
		Expect(err).NotTo(HaveOccurred())
		Expect(kind.DefaultSeam).To(Equal(target.Seam("connection")))
		Expect(kind.Armable).To(BeTrue())
	})
})
