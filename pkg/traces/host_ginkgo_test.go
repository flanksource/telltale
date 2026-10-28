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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/tracing"
)

func openHost(workDir string) *traces.Host {
	host, err := traces.Open(traces.OpenOptions{WorkDir: workDir, RunID: traces.RunID(time.Now())})
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
	var server *httptest.Server
	BeforeEach(func() {
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprint(w, "ok")
		}))
		DeferCleanup(server.Close)
	})

	It("keeps the run's store under .telltale/traces/<runID>, suffixing a run id already taken", func() {
		workDir := GinkgoT().TempDir()
		runID := traces.RunID(time.Date(2026, 10, 15, 9, 30, 0, 0, time.UTC))
		first, err := traces.Open(traces.OpenOptions{WorkDir: workDir, RunID: runID})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(first.Close(context.Background())).To(Succeed()) })
		second, err := traces.Open(traces.OpenOptions{WorkDir: workDir, RunID: runID})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(second.Close(context.Background())).To(Succeed()) })

		Expect([]string{first.RunID(), second.RunID()}).To(Equal([]string{"run-2026-10-15T09-30-00Z", "run-2026-10-15T09-30-00Z-2"}))
		Expect(first.Dir()).To(Equal(filepath.Join(workDir, ".telltale", "traces", first.RunID())))
	})

	It("windows a running capture by its marks, and summarizes each window apart", func() {
		host := openHost(GinkgoT().TempDir())
		capture, err := host.Start("http", json.RawMessage(`{"target":{"seam":"proxy"}}`))
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
		workDir := GinkgoT().TempDir()
		host, err := traces.Open(traces.OpenOptions{WorkDir: workDir, RunID: traces.RunID(time.Now())})
		Expect(err).ToNot(HaveOccurred())
		capture, err := host.Start("http", json.RawMessage(`{"target":{"seam":"proxy"}}`))
		Expect(err).ToNot(HaveOccurred())
		getThrough(capture, server.URL+"/kept")
		end, err := capture.Stop(context.Background())
		Expect(err).ToNot(HaveOccurred())
		ref := capture.Ref(0, end)
		Expect(host.Close(context.Background())).To(Succeed())

		runtime, closeStore, err := traces.Reopen(traces.StoreDir(workDir, host.RunID()))
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(closeStore)
		summary, err := runtime.Summarize(context.Background(), ref)
		Expect(err).ToNot(HaveOccurred())
		Expect(summary).To(HaveKeyWithValue("count", BeEquivalentTo(1)))

		_, _, err = traces.Reopen(traces.StoreDir(workDir, "run-pruned"))
		Expect(err).To(MatchError(os.ErrNotExist))
	})

	It("refuses an unknown kind by naming the registered ones", func() {
		host := openHost(GinkgoT().TempDir())
		_, err := host.Start("smtp", nil)
		Expect(err).To(MatchError(And(ContainSubstring(`unknown trace kind "smtp"`), ContainSubstring("http"), ContainSubstring("sql"))))
	})

	It("refuses params the kind refuses before starting anything", func() {
		host := openHost(GinkgoT().TempDir())
		_, err := host.Start("http", json.RawMessage(`{"target":{"seam":"proxy"},"mode": "sniff"}`))
		Expect(err).To(MatchError(ContainSubstring("trace http params")))
	})
})

var _ = Describe("Prune", func() {
	It("keeps only the newest runs' stores", func() {
		workDir := GinkgoT().TempDir()
		runs := []string{"run-2026-10-13T00-00-00Z", "run-2026-10-14T00-00-00Z", "run-2026-10-15T00-00-00Z"}
		for _, run := range runs {
			Expect(os.MkdirAll(filepath.Join(workDir, traces.Dir, run, "locks"), 0o755)).To(Succeed())
		}
		Expect(traces.Prune(workDir, 2)).To(Succeed())
		entries, err := os.ReadDir(filepath.Join(workDir, traces.Dir))
		Expect(err).ToNot(HaveOccurred())
		var kept []string
		for _, entry := range entries {
			kept = append(kept, entry.Name())
		}
		Expect(kept).To(Equal(runs[1:]))
	})

	It("has nothing to prune in a work dir that never traced", func() {
		Expect(traces.Prune(GinkgoT().TempDir(), traces.KeepRuns)).To(Succeed())
	})
})
