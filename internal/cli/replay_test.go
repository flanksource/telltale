package cli_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"

	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/traces/read"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("trace replay", func() {
	It("reads every selected run without executing its command again", func() {
		var requests atomic.Int64
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requests.Add(1)
			_, _ = w.Write([]byte("recorded"))
		}))
		DeferCleanup(upstream.Close)
		targetURL := upstream.URL + "/recorded"
		dataDir := GinkgoT().TempDir()
		ids := []string{}
		for range 2 {
			run, err := telltale.Start(context.Background(), telltale.StartOptions{DataDir: dataDir, Command: []string{"curl", "--noproxy", "", "-sS", targetURL}, Traces: []telltale.TraceSpec{{Kind: "http", Params: json.RawMessage(`{"target":{"seam":"proxy"}}`)}}})
			Expect(err).NotTo(HaveOccurred())
			_, err = run.Wait()
			Expect(err).NotTo(HaveOccurred())
			ids = append(ids, run.ID())
		}
		for _, selected := range [][]string{ids[:1], ids} {
			args := append([]string{"trace", "replay", "--data-dir", dataDir, "--format", "ndjson"}, selected...)
			stdout, stderr, err := execute(args...)
			Expect(err).NotTo(HaveOccurred(), stderr)
			rows := []read.Record{}
			for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
				var record read.Record
				Expect(json.Unmarshal([]byte(line), &record)).To(Succeed())
				rows = append(rows, record)
			}
			for _, id := range selected {
				Expect(rows).To(ContainElement(And(HaveField("RunID", id), HaveField("Kind", "http"), HaveField("Record", HaveKeyWithValue("url", targetURL)))))
				Expect(rows).To(ContainElement(And(HaveField("RunID", id), HaveField("Kind", "process"), HaveField("Result", "metrics"))))
			}
			Expect(rows).To(HaveEach(HaveField("RunID", BeElementOf(selected))))
		}
		Expect(requests.Load()).To(Equal(int64(2)))
		stdout, _, err := execute("trace", "replay", "--data-dir", dataDir, ids[0], "missing")
		Expect(err).To(HaveOccurred())
		Expect(stdout).To(BeEmpty())
		_, _, err = execute("trace", "replay", "--data-dir", dataDir, ids[0], ids[0])
		Expect(err).To(MatchError(ContainSubstring("selected twice")))
	})

	DescribeTable("rejects invalid replay requests", func(args []string, message string) {
		_, _, err := execute(append([]string{"trace", "replay", "--data-dir", GinkgoT().TempDir()}, args...)...)
		Expect(err).To(MatchError(ContainSubstring(message)))
	},
		Entry("no IDs", nil, "at least 1 arg"),
		Entry("unknown ID", []string{"missing"}, "get run missing"),
		Entry("path traversal", []string{"../escape"}, "not a run id"),
		Entry("bad sort", []string{"missing", "--sort", "invalid"}, "sort"),
	)
})
