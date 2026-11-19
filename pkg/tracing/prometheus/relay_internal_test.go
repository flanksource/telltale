package prometheus

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"time"

	dbcontext "github.com/flanksource/commons-db/context"
	dbkubernetes "github.com/flanksource/commons-db/kubernetes"
	"github.com/flanksource/commons/logger"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/tracingtest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

const proxyExporter = "proxy://exporter.monitoring/probe%3Fapp?port=http&module=http_2xx"
const proxyMetrics = "fixture_metric 1\n"

var _ = Describe("Kubernetes scrape endpoints", func() {
	It("relays only the configured GET with the Kubernetes identity and closes on release", func(ctx SpecContext) {
		endpoints, release, err := resolveEndpoints(kubernetesProxyFixture(ctx), []string{proxyExporter})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(release)
		Expect(endpoints).To(HaveLen(1))
		Expect(endpoints[0].original).To(Equal(proxyExporter))
		relay := endpoints[0].url.String()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, relay+"?module=other", nil)
		Expect(err).NotTo(HaveOccurred())
		request.Header.Set("Authorization", "Bearer caller")
		request.Header.Set("Impersonate-User", "caller")
		response, err := http.DefaultClient.Do(request)
		Expect(err).NotTo(HaveOccurred())
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		body, err := io.ReadAll(response.Body)
		Expect(response.Body.Close()).To(Succeed())
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(Equal(proxyMetrics))

		for _, attempt := range []struct {
			method, address string
			status          int
		}{
			{http.MethodPost, relay, http.StatusMethodNotAllowed},
			{http.MethodGet, relay + "/other", http.StatusNotFound},
		} {
			request, err := http.NewRequestWithContext(ctx, attempt.method, attempt.address, nil)
			Expect(err).NotTo(HaveOccurred())
			response, err := http.DefaultClient.Do(request)
			Expect(err).NotTo(HaveOccurred())
			Expect(response.Body.Close()).To(Succeed())
			Expect(response.StatusCode).To(Equal(attempt.status))
		}
		Expect(release()).To(Succeed())
		response, err = http.DefaultClient.Do(request)
		if response != nil {
			Expect(response.Body.Close()).To(Succeed())
		}
		Expect(err).To(HaveOccurred())
	})

	It("removes the scrape target file and closes the relay when discovery fails", func(ctx SpecContext) {
		dir := GinkgoT().TempDir()
		relay := make(chan string, 1)
		scraper := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/-/ready" {
				w.WriteHeader(http.StatusOK)
				return
			}
			files, err := filepath.Glob(filepath.Join(dir, "*.json"))
			if err == nil && len(files) == 1 {
				groups, err := os.ReadFile(files[0])
				var targets []targetGroup
				if err == nil && json.Unmarshal(groups, &targets) == nil && len(targets) == 1 && len(targets[0].Targets) == 1 {
					relay <- "http://" + targets[0].Targets[0] + targets[0].Labels["__metrics_path__"]
				}
			}
			http.Error(w, "discovery unavailable", http.StatusServiceUnavailable)
		}))
		DeferCleanup(scraper.Close)
		env := tracingtest.NewEnv(map[string]tracing.TracePlugin{
			Kind: NewKind(KindOptions{RunID: "proxy-failure", Server: func(context.Context) (Server, error) {
				return Server{URL: scraper.URL, Job: Job, FileSDDir: dir}, nil
			}}),
		})
		_, err := env.Runtime.Start(kubernetesProxyFixture(ctx), proxyCaptureRequest())
		Expect(err).To(MatchError(ContainSubstring("503")))
		Expect(os.ReadDir(dir)).To(BeEmpty())
		var address string
		Expect(relay).To(Receive(&address))
		response, err := http.Get(address)
		if response != nil {
			Expect(response.Body.Close()).To(Succeed())
		}
		Expect(err).To(HaveOccurred())
	})

	It("stores samples from the authenticated proxy with the original label (live)", func(ctx SpecContext) {
		if os.Getenv("TELLTALE_PROMETHEUS_LIVE") == "" {
			Skip("set TELLTALE_PROMETHEUS_LIVE to run the managed scraper")
		}
		managed := NewManaged(GinkgoT().TempDir())
		DeferCleanup(managed.Release, context.Background())
		env := tracingtest.NewEnv(map[string]tracing.TracePlugin{
			Kind: NewKind(KindOptions{RunID: "proxy-live", Server: managed.Server}),
		})
		capture, err := env.Runtime.Start(kubernetesProxyFixture(ctx), proxyCaptureRequest())
		Expect(err).NotTo(HaveOccurred())
		session := capture.Session()
		tracingtest.Running(session)
		client := newClient(Server{URL: session.Snapshot().Handle, Job: Job})
		Eventually(func(g Gomega) {
			var series struct {
				Result []struct{ Metric map[string]string }
			}
			g.Expect(client.get(ctx, "/api/v1/query", url.Values{"query": {`fixture_metric{telltale_run="proxy-live"}`}}, &series)).To(Succeed())
			g.Expect(series.Result).To(ContainElement(HaveField("Metric", HaveKeyWithValue(LabelEndpoint, proxyExporter))))
		}).WithTimeout(20 * time.Second).Should(Succeed())
		session.Stop("spec")
		Expect(tracingtest.Ended(session).Error).To(BeEmpty())
	})
})

func proxyCaptureRequest() tracing.StartRequest {
	params, err := json.Marshal(Options{Endpoints: []string{proxyExporter}, Interval: "1s"})
	Expect(err).NotTo(HaveOccurred())
	return tracing.StartRequest{Kind: Kind, Params: params}
}

func kubernetesProxyFixture(ctx SpecContext) dbcontext.Context {
	const credential = "fixture-identity"
	const path = "/api/v1/namespaces/monitoring/services/http:exporter:http/proxy/probe%3Fapp"
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+credential || r.Header.Get("Impersonate-User") != "" {
			http.Error(w, "invalid identity", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodGet || r.URL.EscapedPath() != path || r.URL.RawQuery != "module=http_2xx" {
			http.Error(w, "incorrect scrape request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = io.WriteString(w, proxyMetrics)
	}))
	DeferCleanup(upstream.Close)
	config := &rest.Config{Host: upstream.URL, BearerToken: credential, TLSClientConfig: rest.TLSClientConfig{
		CAData: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: upstream.Certificate().Raw}),
	}}
	client := dbkubernetes.NewKubeClient(logger.GetLogger("test"), fake.NewSimpleClientset(), config)
	return dbcontext.NewContext(ctx).WithLocalKubernetes(client)
}
