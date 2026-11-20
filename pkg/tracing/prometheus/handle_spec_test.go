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

	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/prometheus"
	"github.com/flanksource/telltale/pkg/tracing/tracingtest"
)

// scrapeState is what the fake Prometheus reports of one endpoint's last scrape.
type scrapeState struct {
	at     time.Time
	health string
	err    string
}

// fakePrometheus serves the targets API over the target groups it
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
	mux.HandleFunc("GET /api/v1/targets", fake.targets)
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
			"lastScrape": state.at.UTC().Format(time.RFC3339Nano),
		})
	}
	writeJSON(w, map[string]any{"status": "success", "data": map[string]any{"activeTargets": active, "droppedTargets": []any{}}})
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

	It("reports successful and failed targets without emitting scrape records", func() {
		session := start()
		tracingtest.Running(session)
		first := time.Now().Add(-100 * time.Millisecond).Truncate(time.Millisecond)
		fake.scraped(healthy, scrapeState{at: first, health: "up"})
		fake.scraped(failing, scrapeState{at: first, health: "down", err: "connection refused"})
		Eventually(func() string { return string(session.Snapshot().Summary) }).WithTimeout(5 * time.Second).
			Should(And(ContainSubstring(`"up":1`), ContainSubstring(`"down":1`), ContainSubstring("connection refused")))
		session.Stop("spec")
		info := tracingtest.Ended(session)
		Expect(info.Error).To(BeEmpty())
		Expect(info.EventCount).To(BeZero())
		Expect(info.Handle).To(Equal(fake.server.URL))
		Expect(filepath.Glob(filepath.Join(fake.dir, "*.json"))).To(BeEmpty(), "release removes the session's file_sd file")
	})

	It("fails the capture once VictoriaMetrics is unreachable", func() {
		session := start()
		tracingtest.Running(session)
		fake.server.Close()
		info := tracingtest.Ended(session)
		Expect(info.State).To(Equal(query.SessionFailed))
		Expect(info.Error).To(ContainSubstring("VictoriaMetrics"))
	})
})
