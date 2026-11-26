package livestream

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestLivestream(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Live updates") }

var _ = Describe("shared snapshot sampler", func() {
	It("reuses a sample across two subscribers and cancels after the last disconnect", func(ctx SpecContext) {
		var sampler Sampler[string, int64]
		DeferCleanup(sampler.Close)
		var calls atomic.Int64
		cancelled := make(chan struct{})
		load := func(ctx context.Context) (int64, time.Duration, error) {
			context.AfterFunc(ctx, func() { close(cancelled) })
			return calls.Add(1), time.Hour, nil
		}
		follow := func() (context.CancelFunc, <-chan int64, <-chan error) {
			child, cancel := context.WithCancel(ctx)
			got, done := make(chan int64, 1), make(chan error, 1)
			go func() {
				done <- sampler.Follow(child, "fixture", load, func(value int64) error { got <- value; return nil })
			}()
			return cancel, got, done
		}
		first, firstFrame, firstDone := follow()
		Eventually(firstFrame).Should(Receive(Equal(int64(1))))
		second, secondFrame, secondDone := follow()
		Eventually(secondFrame).Should(Receive(Equal(int64(1))))
		first()
		Eventually(firstDone).Should(Receive(MatchError(context.Canceled)))
		Expect(cancelled).NotTo(BeClosed())
		second()
		Eventually(secondDone).Should(Receive(MatchError(context.Canceled)))
		Eventually(cancelled).Should(BeClosed())
	}, SpecTimeout(3*time.Second))
})
