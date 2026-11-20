package prometheus_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"time"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/query"
	_ "github.com/flanksource/commons-db/query/providers"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/prometheus"
	"github.com/flanksource/telltale/pkg/tracing/tracingtest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("prometheus trace kind (live)", func() {
	BeforeEach(func() {
		if os.Getenv("TELLTALE_PROMETHEUS_LIVE") == "" {
			Skip("set TELLTALE_PROMETHEUS_LIVE to install and run VictoriaMetrics")
		}
	})

	It("stores labelled gauge samples in VictoriaMetrics and reads them after restart", func(ctx SpecContext) {
		const gauge, value = "telltale_live_spec_gauge", 42
		metrics := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprintf(w, "# TYPE %s gauge\n%s %d\n", gauge, gauge, value)
		}))
		DeferCleanup(metrics.Close)
		endpoint := metrics.URL + "/metrics"
		dataDir := GinkgoT().TempDir()
		oldState := filepath.Join(dataDir, "services", "prometheus", "state.yaml")
		Expect(os.MkdirAll(filepath.Dir(oldState), 0o700)).To(Succeed())
		Expect(os.WriteFile(oldState, []byte("previous Prometheus state"), 0o600)).To(Succeed())
		managed := prometheus.NewManaged(dataDir)
		DeferCleanup(managed.Release, context.Background())
		env := tracingtest.NewEnv(map[string]tracing.TracePlugin{
			prometheus.Kind: prometheus.NewKind(prometheus.KindOptions{RunID: "run-live", Server: managed.Server}),
		})
		from := time.Now().UTC().Truncate(time.Second)
		session := env.Start(prometheus.Kind, fmt.Sprintf(`{"endpoints": [%q], "interval": "1s", "labels": {"env": "dev"}}`, endpoint))
		tracingtest.Running(session)
		server := session.Snapshot().Handle
		profile := query.Profile{Name: "captured-metrics", Query: gauge + `{telltale_run="run-live"}`,
			Provider: query.ProviderConfig{Type: "prometheus", Options: map[string]any{"url": server}}}
		checkSample := func(g Gomega) {
			result, err := query.Execute(dbcontext.NewContext(ctx), profile)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(result.Rows).To(ContainElement(And(
				HaveKeyWithValue("value", BeNumerically("==", value)), HaveKeyWithValue("env", "dev"),
				HaveKeyWithValue("telltale_run", "run-live"), HaveKeyWithValue("telltale_endpoint", endpoint),
				HaveKeyWithValue("telltale_stream", Not(BeEmpty())),
			)))
		}
		Eventually(checkSample).WithTimeout(20 * time.Second).Should(Succeed())
		session.Stop("spec")
		info := tracingtest.Ended(session)
		Expect(info.Error).To(BeEmpty())
		Expect(info.EventCount).To(BeZero())
		Expect(env.Rows(info)).To(BeEmpty())
		frames, err := env.Results.Backend.Meta(ctx, info.Events.Stream+"-frames")
		Expect(err).NotTo(HaveOccurred())
		Expect(frames.HighSeq).To(BeZero())
		paths, err := prometheus.Paths(dataDir)
		Expect(err).NotTo(HaveOccurred())
		Expect(filepath.Glob(filepath.Join(paths.FileSD, "*.json"))).To(BeEmpty())
		to := time.Now().UTC().Add(time.Second).Truncate(time.Second)
		Expect(managed.Release(ctx)).To(Succeed())
		Expect(http.Get(server + "/health")).Error().To(HaveOccurred())
		restarted, err := managed.Server(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(restarted.URL).To(Equal(server))
		profile.Provider.Options["range"] = map[string]any{
			"start": from.Format(time.RFC3339), "end": to.Format(time.RFC3339), "step": "1s",
		}
		checkSample(Default)
		Expect(os.ReadFile(oldState)).To(Equal([]byte("previous Prometheus state")))
	})
})
