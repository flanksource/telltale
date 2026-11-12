package telltale_test

import (
	"context"
	"time"

	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/tracing/processcapture"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("run capture capacity", func() {
	It("arms every requested capture alongside the automatic process captures", func() {
		const requested = 6
		specs := make([]telltale.TraceSpec, requested)
		for i := range specs {
			specs[i].Kind = processcapture.Kind
		}
		run, err := telltale.Start(context.Background(), telltale.StartOptions{
			DataDir: GinkgoT().TempDir(), PID: sleeper("30").Process.Pid,
			Duration: 100 * time.Millisecond, Traces: specs,
		})
		Expect(err).NotTo(HaveOccurred())
		result, err := run.Wait()
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Captures).To(HaveLen(requested))
		Expect(result.Process).NotTo(BeNil())
	})
})
