package groups_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"

	"github.com/flanksource/telltale/pkg/tracing/groups"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("NetMonitor", func() {
	It("counts the bytes this process moves after nettop's first block", func(ctx SpecContext) {
		payload := strings.Repeat("x", 256<<10)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(payload)) }))
		DeferCleanup(server.Close)
		monitor, err := groups.StartNetMonitor(ctx, time.Second)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(monitor.Close)

		Eventually(func(g Gomega) {
			response, err := http.Get(server.URL)
			g.Expect(err).NotTo(HaveOccurred())
			_ = response.Body.Close()
			counters, err := monitor.Counters()
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(counters[os.Getpid()].Rx).To(BeNumerically(">", 0))
		}).WithTimeout(20 * time.Second).WithPolling(time.Second).Should(Succeed())
	}, SpecTimeout(30*time.Second))
})
