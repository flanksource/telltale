package lifetime_test

import (
	"errors"
	"testing"
	"time"

	"github.com/flanksource/clicky/task"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/flanksource/telltale/internal/lifetime"
)

func TestLifetime(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Lifetime")
}

var _ = Describe("a lifetime service", func() {
	It("starts while long-lived services hold every task worker", func() {
		const workers = 2
		task.SetMaxConcurrent(workers)
		DeferCleanup(task.SetMaxConcurrent, 0)
		release := make(chan struct{})
		DeferCleanup(func() { close(release) })
		for range workers + 1 {
			lifetime.Start("holds", func() error { <-release; return nil })
		}
		started := make(chan struct{})
		lifetime.Start("serves", func() error { close(started); return nil })
		Eventually(started).WithTimeout(2 * time.Second).Should(BeClosed())
	})

	It("reports what its function returned once it returned", func() {
		failed := errors.New("listener closed")
		service := lifetime.Start("fails", func() error { return failed })
		Eventually(service.Done()).Should(BeClosed())
		Expect(service.Wait()).To(MatchError(failed))
	})
})
