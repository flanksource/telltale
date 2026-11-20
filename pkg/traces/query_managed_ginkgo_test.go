package traces_test

import (
	"encoding/json"
	"fmt"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/flanksource/telltale/pkg/tracing/prometheus"
)

var _ = Describe("the query kind over VictoriaMetrics (live)", func() {
	BeforeEach(func() {
		if os.Getenv("TELLTALE_PROMETHEUS_LIVE") == "" {
			Skip("set TELLTALE_PROMETHEUS_LIVE to install and run VictoriaMetrics")
		}
	})

	It("starts and holds VictoriaMetrics for a run that only queries it", func() {
		params := json.RawMessage(fmt.Sprintf(`{"provider":{"type":"prometheus","connection":"connection://%s","query":"vector(1)"}}`, prometheus.ConnectionName))
		Expect(readOnce(GinkgoT().TempDir(), params)).To(HaveKeyWithValue("count", BeNumerically("==", 1)))
	})
})
