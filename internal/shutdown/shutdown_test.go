package shutdown_test

import (
	"context"
	"io"
	"os"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/flanksource/telltale/internal/shutdown"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestShutdown(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Command shutdown") }

var _ = Describe("Command lifecycle", func() {
	It("cancels on the first signal and forces exit on the second after restoring the terminal", func() {
		signals, exits := make(chan os.Signal, 2), make(chan int, 1)
		var restored atomic.Bool
		controller, err := shutdown.New(context.Background(), shutdown.Options{
			GracePeriod: time.Hour, Signals: signals, Output: io.Discard,
			Restore: func(bool) { restored.Store(true) },
			ForceExit: func(code int) {
				if !restored.Load() {
					code = -1
				}
				exits <- code
			},
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(controller.Close)
		signals <- os.Interrupt
		Eventually(controller.Context().Done()).Should(BeClosed())
		Expect(exits).NotTo(Receive())
		signals <- syscall.SIGTERM
		Eventually(exits).Should(Receive(Equal(143)))
	})

	It("starts the same deadline for a terminal quit request and exits with an error on expiry", func() {
		exits := make(chan int, 1)
		controller, err := shutdown.New(context.Background(), shutdown.Options{
			GracePeriod: 40 * time.Millisecond, Signals: make(chan os.Signal), Output: io.Discard,
			Restore: func(bool) {}, ForceExit: func(code int) { exits <- code },
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(controller.Close)
		Expect(shutdown.Request(controller.Context())).To(Succeed())
		Eventually(controller.Context().Done()).Should(BeClosed())
		Eventually(exits).Should(Receive(Equal(1)))
	})

	It("disarms the watchdog after graceful completion", func() {
		signals, exits := make(chan os.Signal, 2), make(chan int, 1)
		controller, err := shutdown.New(context.Background(), shutdown.Options{
			GracePeriod: 40 * time.Millisecond, Signals: signals, Output: io.Discard,
			Restore: func(bool) {}, ForceExit: func(code int) { exits <- code },
		})
		Expect(err).NotTo(HaveOccurred())
		signals <- os.Interrupt
		Eventually(controller.Context().Done()).Should(BeClosed())
		controller.Close()
		Consistently(exits, 100*time.Millisecond).ShouldNot(Receive())
	})

	It("requires an installed controller for a terminal quit request", func() {
		Expect(shutdown.Request(context.Background())).To(MatchError(ContainSubstring("shutdown controller")))
	})
})
