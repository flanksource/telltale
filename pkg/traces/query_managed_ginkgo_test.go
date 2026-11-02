package traces_test

import (
	"encoding/json"
	"fmt"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/flanksource/telltale/pkg/tracing/prometheus"
)

// Set TELLTALE_PROMETHEUS_LIVE=1 to run it: the first run downloads
// Prometheus prometheus.Version into ~/.deps.
var _ = Describe("the query kind over the managed Prometheus (live)", func() {
	BeforeEach(func() {
		if os.Getenv("TELLTALE_PROMETHEUS_LIVE") == "" {
			Skip("TELLTALE_PROMETHEUS_LIVE is not set; this spec downloads and starts Prometheus " + prometheus.Version)
		}
	})

	It("starts and holds the data dir's managed Prometheus for a run that only queries it", func() {
		params := json.RawMessage(fmt.Sprintf(`{"provider":{"type":"prometheus","connection":"connection://%s","query":"vector(1)"}}`, prometheus.ConnectionName))
		Expect(readOnce(GinkgoT().TempDir(), params)).To(HaveKeyWithValue("count", BeNumerically("==", 1)))
	})
})
