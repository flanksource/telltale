package prometheus_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/flanksource/telltale/pkg/tracing/prometheus"
)

var _ = Describe("managed prometheus paths", func() {
	It("roots a relative data dir at the working directory, because deps takes only an absolute file_sd dir", func() {
		relative := filepath.Join(".tmp", "data")
		cwd, err := os.Getwd()
		Expect(err).NotTo(HaveOccurred())

		paths, err := prometheus.Paths(relative)

		Expect(err).NotTo(HaveOccurred())
		Expect(paths).To(Equal(prometheus.ManagedPaths{
			State:  filepath.Join(cwd, relative, "services"),
			FileSD: filepath.Join(cwd, relative, "prometheus", "targets"),
		}))
	})
})
