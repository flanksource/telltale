package telltale_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/recordstore/recordresults"
	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/target"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type shutdownReading struct {
	Reason string `json:"reason"`
}
type shutdownCapture struct{ started, release chan struct{} }

var shutdownKindID atomic.Int64

func (shutdownCapture) Params() struct{} { return struct{}{} }
func (shutdownCapture) Schema() recordresults.ResultType[shutdownReading] {
	return recordresults.ResultType[shutdownReading]{Title: "Shutdown checkpoints"}
}
func (shutdownCapture) Handle(ctx dbcontext.Context, _ struct{}, _ tracing.Emitter[shutdownReading], _ tracing.Records[shutdownReading]) error {
	<-ctx.Done()
	return nil
}
func (c shutdownCapture) Checkpoint(ctx dbcontext.Context, _ struct{}, out tracing.Emitter[shutdownReading]) error {
	var request shutdownReading
	if _, err := tracing.DecodeCheckpointParams(ctx, &request); err != nil {
		return err
	}
	if request.Reason == "interval" {
		close(c.started)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.release:
		}
	}
	return out.Emit(ctx, request)
}

var _ = Describe("Run shutdown checkpoints", func() {
	It("cancels an interval reading and preserves the final stop reading and manifest", func() {
		capture := shutdownCapture{started: make(chan struct{}), release: make(chan struct{})}
		DeferCleanup(func() { close(capture.release) })
		kind := fmt.Sprintf("shutdown_checkpoint_%d", shutdownKindID.Add(1))
		Expect(traces.Register(traces.Registration{
			Name: kind, Title: "Shutdown checkpoints", Summary: "Records shutdown checkpoint lifecycle", Seams: []target.Seam{target.Host}, DefaultSeam: target.Host,
			Checkpoint: shutdownReading{}, New: func(traces.KindDeps) tracing.TracePlugin {
				return tracing.NewHandler[struct{}, shutdownReading](capture, tracing.Capabilities{Live: true}, target.Host)
			},
		})).To(Succeed())
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		dataDir := GinkgoT().TempDir()
		run, err := telltale.Start(ctx, telltale.StartOptions{DataDir: dataDir, Every: 10 * time.Millisecond, Traces: []telltale.TraceSpec{{Kind: kind}}})
		Expect(err).NotTo(HaveOccurred())
		Eventually(capture.started).Should(BeClosed())
		cancel()
		Eventually(run.Done(), time.Second).Should(BeClosed())
		result, err := run.Wait()
		Expect(err).NotTo(HaveOccurred())
		Expect(result.CheckpointErrors).To(BeEmpty())
		Expect(result.Captures).To(ConsistOf(HaveField("Summary", HaveKeyWithValue("count", BeNumerically(">", 0)))))
		info, err := telltale.Get(telltale.GetOptions{DataDir: dataDir, RunID: run.ID()})
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Status).To(Equal(telltale.StatusSucceeded))
	})
})
