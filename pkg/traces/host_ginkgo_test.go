package traces_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/tracing"
)

func openHost(dataDir string) *traces.Host {
	host, err := traces.Open(traces.OpenOptions{DataDir: dataDir, RunID: traces.RunID(time.Now())})
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() { Expect(host.Close(context.Background())).To(Succeed()) })
	return host
}

// getThrough fetches url through the proxy a capture exported.
func getThrough(capture *traces.Capture, target string) {
	proxy, err := url.Parse(capture.Exports()["HTTP_PROXY"])
	Expect(err).ToNot(HaveOccurred())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxy)}, Timeout: 5 * time.Second}
	response, err := client.Get(target)
	Expect(err).ToNot(HaveOccurred())
	Expect(response.Body.Close()).To(Succeed())
}

var _ = Describe("trace host", func() {
	It("names a run with at most nine hostname characters and a compact UTC timestamp", func() {
		hostname, err := os.Hostname()
		Expect(err).NotTo(HaveOccurred())
		prefix := strings.ReplaceAll(strings.Split(hostname, ".")[0], "-", "")
		prefix = prefix[:min(9, len(prefix))]
		started := time.Date(2026, 10, 15, 11, 30, 0, 0, time.FixedZone("example", 2*60*60))
		Expect(traces.RunID(started)).To(Equal(prefix + "20261015T093000Z"))
	})
	var server *httptest.Server
	BeforeEach(func() {
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprint(w, "ok")
		}))
		DeferCleanup(server.Close)
	})

	It("keeps the run's store under <data dir>/traces/<runID>, suffixing a run id already taken", func() {
		dataDir := GinkgoT().TempDir()
		runID := traces.RunID(time.Date(2026, 10, 15, 9, 30, 0, 0, time.UTC))
		first, err := traces.Open(traces.OpenOptions{DataDir: dataDir, RunID: runID})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(first.Close(context.Background())).To(Succeed()) })
		second, err := traces.Open(traces.OpenOptions{DataDir: dataDir, RunID: runID})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(second.Close(context.Background())).To(Succeed()) })

		Expect([]string{first.RunID(), second.RunID()}).To(Equal([]string{runID, runID + "_2"}))
		Expect(first.Dir()).To(Equal(filepath.Join(dataDir, "traces", first.RunID())))
	})

	It("labels a capture's session with the labels it is started with", func() {
		host := openHost(GinkgoT().TempDir())
		labels := map[string]string{"telltale_entry": "checkout-proxy-http-1", "telltale_definition": "checkout"}
		capture, err := host.Start(traces.StartOptions{Kind: "http", Params: json.RawMessage(`{"target":{"seam":"proxy"}}`), Labels: labels})
		Expect(err).ToNot(HaveOccurred())
		Expect(host.Runtime().Sessions.List()).To(ContainElement(And(
			HaveField("Events.Stream", capture.Stream()), HaveField("Labels", HaveKeyWithValue("telltale_entry", "checkout-proxy-http-1")),
			HaveField("Labels", HaveKeyWithValue("telltale_definition", "checkout")),
		)))
	})

	It("windows a running capture by its marks, and summarizes each window apart", func() {
		host := openHost(GinkgoT().TempDir())
		capture, err := host.Start(traces.StartOptions{Kind: "http", Params: json.RawMessage(`{"target":{"seam":"proxy"}}`)})
		Expect(err).ToNot(HaveOccurred())
		Expect(capture.Exports()).To(HaveKey("HTTPS_PROXY"))

		before, err := capture.Mark(context.Background())
		Expect(err).ToNot(HaveOccurred())
		getThrough(capture, server.URL+"/first")
		getThrough(capture, server.URL+"/second")
		middle, err := capture.Mark(context.Background())
		Expect(err).ToNot(HaveOccurred())
		getThrough(capture, server.URL+"/third")
		end, err := capture.Stop(context.Background())
		Expect(err).ToNot(HaveOccurred())

		first, err := host.Summarize(context.Background(), capture.Ref(before, middle))
		Expect(err).ToNot(HaveOccurred())
		Expect(first).To(HaveKeyWithValue("count", BeEquivalentTo(2)))
		last, err := host.Summarize(context.Background(), capture.Ref(middle, end))
		Expect(err).ToNot(HaveOccurred())
		Expect(last).To(HaveKeyWithValue("count", BeEquivalentTo(1)))
		Expect(capture.Ref(middle, end)).To(Equal(tracing.TraceRef{Kind: "http", Stream: capture.Stream(), AfterSeq: middle, ToSeq: end}))
	})

	It("reopens a finished run's store to serve it, and finds no store a prune removed", func() {
		dataDir := GinkgoT().TempDir()
		host, err := traces.Open(traces.OpenOptions{DataDir: dataDir, RunID: traces.RunID(time.Now())})
		Expect(err).ToNot(HaveOccurred())
		capture, err := host.Start(traces.StartOptions{Kind: "http", Params: json.RawMessage(`{"target":{"seam":"proxy"}}`)})
		Expect(err).ToNot(HaveOccurred())
		getThrough(capture, server.URL+"/kept")
		end, err := capture.Stop(context.Background())
		Expect(err).ToNot(HaveOccurred())
		ref := capture.Ref(0, end)
		Expect(host.Close(context.Background())).To(Succeed())

		runtime, closeStore, err := traces.Reopen(traces.StoreDir(dataDir, host.RunID()))
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(closeStore)
		summary, err := runtime.Summarize(context.Background(), ref)
		Expect(err).ToNot(HaveOccurred())
		Expect(summary).To(HaveKeyWithValue("count", BeEquivalentTo(1)))

		_, _, err = traces.Reopen(traces.StoreDir(dataDir, "run-pruned"))
		Expect(err).To(MatchError(os.ErrNotExist))
	})

	It("refuses an unknown kind by naming the registered ones", func() {
		host := openHost(GinkgoT().TempDir())
		_, err := host.Start(traces.StartOptions{Kind: "smtp"})
		Expect(err).To(MatchError(And(ContainSubstring(`unknown trace kind "smtp"`), ContainSubstring("http"), ContainSubstring("sql"))))
	})

	It("refuses params the kind refuses before starting anything", func() {
		host := openHost(GinkgoT().TempDir())
		_, err := host.Start(traces.StartOptions{Kind: "http", Params: json.RawMessage(`{"target":{"seam":"proxy"},"mode": "sniff"}`)})
		Expect(err).To(MatchError(ContainSubstring("trace http params")))
	})
})

var _ = Describe("Prune", func() {
	It("retains the newest start times across hostnames and old IDs", func() {
		dataDir := GinkgoT().TempDir()
		runs := []string{"zworker20261105T000000Z", "run-2026-11-06T00-00-00Z", "aworker20261107T000000Z"}
		for index, run := range runs {
			dir := traces.StoreDir(dataDir, run)
			Expect(os.MkdirAll(dir, 0o755)).To(Succeed())
			data, err := json.Marshal(map[string]any{"startedAt": time.Date(2026, 11, 5+index, 0, 0, 0, 0, time.UTC)})
			Expect(err).NotTo(HaveOccurred())
			Expect(os.WriteFile(filepath.Join(dir, traces.ManifestFile), data, 0o600)).To(Succeed())
		}
		Expect(traces.Prune(dataDir, 2)).To(Succeed())
		Expect(os.Stat(traces.StoreDir(dataDir, runs[0]))).Error().To(MatchError(os.ErrNotExist))
		for _, id := range runs[1:] {
			Expect(os.Stat(traces.StoreDir(dataDir, id))).Error().NotTo(HaveOccurred())
		}
	})
	It("keeps only the newest runs' stores", func() {
		dataDir := GinkgoT().TempDir()
		runs := []string{"run-2026-10-13T00-00-00Z", "run-2026-10-14T00-00-00Z", "run-2026-10-15T00-00-00Z"}
		for _, run := range runs {
			Expect(os.MkdirAll(filepath.Join(dataDir, traces.Dir, run, "locks"), 0o755)).To(Succeed())
		}
		Expect(traces.Prune(dataDir, 2)).To(Succeed())
		entries, err := os.ReadDir(filepath.Join(dataDir, traces.Dir))
		Expect(err).ToNot(HaveOccurred())
		var kept []string
		for _, entry := range entries {
			kept = append(kept, entry.Name())
		}
		Expect(kept).To(Equal(runs[1:]))
	})

	It("has nothing to prune in a data dir that never traced", func() {
		Expect(traces.Prune(GinkgoT().TempDir(), traces.KeepRuns)).To(Succeed())
	})
})
