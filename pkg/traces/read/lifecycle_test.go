package read_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/traces/read"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("live run manifest", func() {
	It("still returns the final result after the run context is cancelled", func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		run, err := telltale.Start(ctx, telltale.StartOptions{WorkDir: GinkgoT().TempDir(), PID: os.Getpid(), Duration: 5 * time.Second, Traces: []telltale.TraceSpec{{Kind: "process_samples"}}})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(run.Stop()).To(Succeed()); _, err := run.Wait(); Expect(err).NotTo(HaveOccurred()) })
		result, err := read.Wait(ctx, run, true, func(query.Row) error { cancel(); return nil })
		Expect(err).NotTo(HaveOccurred())
		Expect(result).NotTo(BeNil())
		Expect(result.Captures).To(ConsistOf(HaveField("Kind", "process_samples")))
	})
	It("publishes seq-zero refs at start and replaces them with the compatible final result", func() {
		run, err := telltale.Start(context.Background(), telltale.StartOptions{WorkDir: GinkgoT().TempDir(), PID: os.Getpid(), Duration: time.Second, Traces: []telltale.TraceSpec{{Kind: "process_samples"}}})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(run.Stop()).To(Succeed()); _, err := run.Wait(); Expect(err).NotTo(HaveOccurred()) })
		data, err := os.ReadFile(filepath.Join(run.Dir(), telltale.ManifestFile))
		Expect(err).NotTo(HaveOccurred())
		var live telltale.RunInfo
		Expect(json.Unmarshal(data, &live)).To(Succeed())
		Expect(live.Status).To(Equal(telltale.StatusRunning))
		Expect(live.EndedAt.IsZero()).To(BeTrue())
		Expect(live.ExitCode).To(BeNil())
		Expect(live.Captures).To(ConsistOf(And(HaveField("Kind", "process_samples"), HaveField("Ref.AfterSeq", int64(0)), HaveField("Ref.ToSeq", int64(0)), HaveField("Ref.Stream", Not(BeEmpty())))))
		result, err := run.Wait()
		Expect(err).NotTo(HaveOccurred())
		final, err := telltale.ReadManifest(run.Dir())
		Expect(err).NotTo(HaveOccurred())
		Expect(final.RunID).To(Equal(result.RunID))
		Expect(final.EndedAt.IsZero()).To(BeFalse())
		Expect(final.Captures[0].Ref.ToSeq).To(BeNumerically(">", 0))
	})
})
