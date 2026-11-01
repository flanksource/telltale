package prometheus_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"

	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/prometheus"
	"github.com/flanksource/telltale/pkg/tracing/tracingtest"
)

// scrapeState is what the fake Prometheus reports of one endpoint's last scrape.
type scrapeState struct {
	at       time.Time
	health   string
	err      string
	duration float64
	samples  int
}

// fakePrometheus serves the targets and query APIs over the target groups it
// reads from the file_sd dir, reporting the scrape each endpoint is given.
type fakePrometheus struct {
	dir     string
	mu      sync.Mutex
	scrapes map[string]scrapeState
	server  *httptest.Server
}

func newFakePrometheus(dir string) *fakePrometheus {
	fake := &fakePrometheus{dir: dir, scrapes: map[string]scrapeState{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /-/ready", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ready")) })
	mux.HandleFunc("GET /api/v1/targets", fake.targets)
	mux.HandleFunc("GET /api/v1/query", fake.query)
	fake.server = httptest.NewServer(mux)
	DeferCleanup(fake.server.Close)
	return fake
}

func (f *fakePrometheus) scraped(endpoint string, state scrapeState) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scrapes[endpoint] = state
}

// groups are the target groups of every file in the dir.
func (f *fakePrometheus) groups() []map[string]string {
	files, err := filepath.Glob(filepath.Join(f.dir, "*.json"))
	Expect(err).ToNot(HaveOccurred())
	var labels []map[string]string
	for _, file := range files {
		data, err := os.ReadFile(file)
		Expect(err).ToNot(HaveOccurred())
		var groups []struct {
			Labels map[string]string `json:"labels"`
		}
		Expect(json.Unmarshal(data, &groups)).To(Succeed())
		for _, group := range groups {
			labels = append(labels, group.Labels)
		}
	}
	return labels
}

func (f *fakePrometheus) targets(w http.ResponseWriter, r *http.Request) {
	defer GinkgoRecover()
	Expect(r.URL.Query().Get("scrapePool")).To(Equal("file_sd"))
	f.mu.Lock()
	defer f.mu.Unlock()
	active := []map[string]any{}
	for _, labels := range f.groups() {
		state, ok := f.scrapes[labels["telltale_endpoint"]]
		if !ok {
			state = scrapeState{health: "unknown"}
		}
		active = append(active, map[string]any{
			"labels":     map[string]string{"job": "file_sd", "telltale_endpoint": labels["telltale_endpoint"], "telltale_stream": labels["telltale_stream"]},
			"scrapePool": "file_sd", "health": state.health, "lastError": state.err,
			"lastScrape": state.at.UTC().Format(time.RFC3339Nano), "lastScrapeDuration": state.duration,
		})
	}
	writeJSON(w, map[string]any{"status": "success", "data": map[string]any{"activeTargets": active, "droppedTargets": []any{}}})
}

func (f *fakePrometheus) query(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := []map[string]any{}
	for endpoint, state := range f.scrapes {
		if state.at.IsZero() {
			continue
		}
		at := float64(state.at.UnixMilli()) / 1000
		result = append(result, map[string]any{
			"metric": map[string]string{"telltale_endpoint": endpoint},
			"values": []any{[]any{at, fmt.Sprint(state.samples)}},
		})
	}
	writeJSON(w, map[string]any{"status": "success", "data": map[string]any{"resultType": "matrix", "result": result}})
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	Expect(json.NewEncoder(w).Encode(value)).To(Succeed())
}

var _ = Describe("prometheus capture", func() {
	const healthy, failing = "http://localhost:18080/metrics", "http://localhost:18081/metrics"
	var fake *fakePrometheus
	var env tracingtest.Env

	BeforeEach(func() {
		dir := GinkgoT().TempDir()
		fake = newFakePrometheus(dir)
		server := func(context.Context) (prometheus.Server, error) {
			return prometheus.Server{URL: fake.server.URL, Job: "file_sd", FileSDDir: dir}, nil
		}
		env = tracingtest.NewEnv(map[string]tracing.TracePlugin{
			prometheus.Kind: prometheus.NewKind(prometheus.KindOptions{RunID: "run-spec", Server: server}),
		})
	})

	start := func() *query.Session {
		GinkgoHelper()
		return env.Start(prometheus.Kind, fmt.Sprintf(`{"endpoints": [%q, %q], "interval": "1s"}`, healthy, failing))
	}

	It("records one scrape each time a target's last scrape advances, an error target's with its error", func() {
		session := start()
		tracingtest.Running(session)
		first := time.Now().Add(-100 * time.Millisecond).Truncate(time.Millisecond)
		fake.scraped(healthy, scrapeState{at: first, health: "up", duration: 0.0042, samples: 7})
		fake.scraped(failing, scrapeState{at: first, health: "down", err: "connection refused", duration: 0.001})
		Eventually(func() int64 { return session.Snapshot().EventCount }).WithTimeout(5 * time.Second).Should(Equal(int64(2)))
		Consistently(func() int64 { return session.Snapshot().EventCount }).WithTimeout(1200 * time.Millisecond).Should(Equal(int64(2)))

		fake.scraped(healthy, scrapeState{at: first.Add(time.Second), health: "up", duration: 0.003, samples: 9})
		Eventually(func() int64 { return session.Snapshot().EventCount }).WithTimeout(5 * time.Second).Should(Equal(int64(3)))
		session.Stop("spec")
		info := tracingtest.Ended(session)
		Expect(info.Error).To(BeEmpty())

		scrape := func(endpoint, health string, samples int, scrapeErr string) OmegaMatcher {
			return MatchKeys(IgnoreExtras, Keys{
				"endpoint": Equal(endpoint), "health": Equal(health), "samples": BeNumerically("==", samples), "error": Equal(scrapeErr),
			})
		}
		Expect(env.Rows(info)).To(ConsistOf(
			scrape(healthy, "up", 7, ""),
			scrape(failing, "down", 0, "connection refused"),
			scrape(healthy, "up", 9, ""),
		))
		Expect(info.Handle).To(Equal(fake.server.URL))
		Expect(filepath.Glob(filepath.Join(fake.dir, "*.json"))).To(BeEmpty(), "release removes the session's file_sd file")
	})

	It("fails the capture loudly once Prometheus is unreachable", func() {
		session := start()
		tracingtest.Running(session)
		fake.server.Close()
		info := tracingtest.Ended(session)
		Expect(info.State).To(Equal(query.SessionFailed))
		Expect(info.Error).To(ContainSubstring("prometheus"))
	})
})
