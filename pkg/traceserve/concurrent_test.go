package traceserve_test

import (
	"context"
	"sync/atomic"
	"time"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/recordstore/recordresults"
	"github.com/flanksource/telltale/pkg/traceserve"
	"github.com/flanksource/telltale/pkg/tracing"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type concurrentParams struct{}

func (concurrentParams) Validate() error { return nil }

type concurrentRow struct {
	At time.Time `json:"at"`
}

type concurrentKind struct {
	pause   atomic.Bool
	entered chan struct{}
	release chan struct{}
}

func (*concurrentKind) Params() concurrentParams { return concurrentParams{} }
func (k *concurrentKind) Schema() recordresults.ResultType[concurrentRow] {
	if k.pause.CompareAndSwap(true, false) {
		close(k.entered)
		<-k.release
	}
	return recordresults.ResultType[concurrentRow]{Title: "Concurrent open", TimeColumn: "at"}
}
func (*concurrentKind) Handle(dbcontext.Context, concurrentParams, tracing.Emitter[concurrentRow], tracing.Records[concurrentRow]) error {
	return nil
}

var _ = Describe("concurrent store opens", func() {
	It("declares all schemas before another reader can share its SQLite backend", func() {
		kinds := tracing.NewKinds()
		first := &concurrentKind{}
		last := &concurrentKind{entered: make(chan struct{}), release: make(chan struct{})}
		Expect(kinds.RegisterKind("alpha", tracing.NewHandler[concurrentParams, concurrentRow](first, tracing.Capabilities{Live: true}, "self"))).To(Succeed())
		Expect(kinds.RegisterKind("zeta", tracing.NewHandler[concurrentParams, concurrentRow](last, tracing.Capabilities{Live: true}, "self"))).To(Succeed())
		last.pause.Store(true)
		options := traceserve.OpenOptions{Dir: GinkgoT().TempDir(), Kinds: kinds, Retention: time.Hour}
		type opened struct {
			runtime *tracing.Runtime
			close   func() error
			err     error
		}
		open := func() opened {
			runtime, closeStore, err := traceserve.Open(options)
			return opened{runtime, closeStore, err}
		}
		pending := make(chan opened, 1)
		go func() { pending <- open() }()
		Eventually(last.entered).WithTimeout(3 * time.Second).Should(BeClosed())
		second := open()
		close(last.release)
		var initial opened
		Eventually(pending).WithTimeout(3 * time.Second).Should(Receive(&initial))
		for _, result := range []opened{initial, second} {
			if result.close != nil {
				DeferCleanup(func() {
					Expect(result.runtime.Sessions.StopAll(context.Background())).To(Succeed())
					Expect(result.close()).To(Succeed())
				})
			}
			Expect(result.err).NotTo(HaveOccurred())
		}
	})
})
