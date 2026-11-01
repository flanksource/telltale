// A live spec of the prometheus kind against the managed Prometheus it starts
// through deps: an endpoint's gauge is scraped into its TSDB with the
// session's labels.

package prometheus_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/flanksource/deps/start"
	"github.com/flanksource/deps/start/state"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/prometheus"
	"github.com/flanksource/telltale/pkg/tracing/tracingtest"
)

// Set TELLTALE_PROMETHEUS_LIVE=1 to run it: the first run downloads
// Prometheus prometheus.Version into ~/.deps.
var _ = Describe("prometheus trace kind (live)", func() {
	const gauge, value = "telltale_live_spec_gauge", 42

	BeforeEach(func() {
		if os.Getenv("TELLTALE_PROMETHEUS_LIVE") == "" {
			Skip("TELLTALE_PROMETHEUS_LIVE is not set; this spec downloads and starts Prometheus " + prometheus.Version)
		}
	})

	It("scrapes an endpoint's gauge into the managed Prometheus with the session's labels", func() {
		metrics := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprintf(w, "# TYPE %s gauge\n%s %d\n", gauge, gauge, value)
		}))
		DeferCleanup(metrics.Close)
		endpoint := metrics.URL + "/metrics"

		dataDir := GinkgoT().TempDir()
		paths, err := prometheus.Paths(dataDir)
		Expect(err).NotTo(HaveOccurred())
		managed := prometheus.NewManaged(dataDir)
		DeferCleanup(func() {
			Expect(managed.Release(context.Background())).To(Succeed())
			Expect(start.Status(context.Background(), "prometheus", start.WithStateDir(paths.State))).
				To(Equal(state.StatusStopped), "the last release stops the Prometheus this process supervises")
		})
		env := tracingtest.NewEnv(map[string]tracing.TracePlugin{
			prometheus.Kind: prometheus.NewKind(prometheus.KindOptions{RunID: "run-live", Server: managed.Server}),
		})
		session := env.Start(prometheus.Kind, fmt.Sprintf(`{"endpoints": [%q], "interval": "1s", "labels": {"env": "dev"}}`, endpoint))
		tracingtest.Running(session)
		Eventually(func() int64 { return session.Snapshot().EventCount }).WithTimeout(30 * time.Second).Should(BeNumerically(">=", 2))
		server := session.Snapshot().Handle

		var gauges struct {
			Result []struct {
				Metric map[string]string `json:"metric"`
				Value  [2]any            `json:"value"`
			} `json:"result"`
		}
		apiGet(server, "/api/v1/query", url.Values{"query": {gauge + `{telltale_run="run-live"}`}}, &gauges)
		var targets struct {
			ActiveTargets []struct {
				Labels         map[string]string `json:"labels"`
				ScrapeInterval string            `json:"scrapeInterval"`
			} `json:"activeTargets"`
		}
		apiGet(server, "/api/v1/targets", url.Values{"scrapePool": {prometheus.Job}}, &targets)
		session.Stop("spec")
		info := tracingtest.Ended(session)
		Expect(info.Error).To(BeEmpty())

		Expect(gauges.Result).To(HaveLen(1))
		Expect(gauges.Result[0].Metric).To(Equal(map[string]string{
			"__name__": gauge, "job": prometheus.Job, "instance": metrics.Listener.Addr().String(), "env": "dev",
			"telltale_run": "run-live", "telltale_stream": info.Events.Stream, "telltale_endpoint": endpoint,
		}))
		Expect(gauges.Result[0].Value[1]).To(Equal(fmt.Sprint(value)))
		var intervals []string
		for _, t := range targets.ActiveTargets {
			if t.Labels["telltale_stream"] == info.Events.Stream {
				intervals = append(intervals, t.ScrapeInterval)
			}
		}
		Expect(intervals).To(Equal([]string{"1000ms"}), "the target's own __scrape_interval__ label, not the global 15s, sets its interval")
		rows := env.Rows(info)
		Expect(rows[0]).To(HaveKeyWithValue("health", "up"))
		Expect(rows[0]).To(HaveKeyWithValue("samples", BeNumerically(">", 0)))
	})
})

// apiGet decodes the data of a Prometheus API answer into data.
func apiGet(server, path string, query url.Values, data any) {
	GinkgoHelper()
	response, err := http.Get(server + path + "?" + query.Encode())
	Expect(err).ToNot(HaveOccurred())
	defer func() { _ = response.Body.Close() }()
	var answer struct {
		Status string          `json:"status"`
		Data   json.RawMessage `json:"data"`
	}
	Expect(json.NewDecoder(response.Body).Decode(&answer)).To(Succeed())
	Expect(answer.Status).To(Equal("success"))
	Expect(json.Unmarshal(answer.Data, data)).To(Succeed())
}
