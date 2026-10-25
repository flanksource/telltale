package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/flanksource/telltale/internal/server"
	"github.com/flanksource/telltale/internal/tracerun"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("serving recorded run stores", func() {
	var mount *server.TraceMount
	var workDir string

	BeforeEach(func() {
		workDir = GinkgoT().TempDir()
		mount = server.NewTraceMount(workDir)
		DeferCleanup(func() { Expect(mount.Close()).To(Succeed()) })
	})

	get := func(path string) (int, string) {
		response := httptest.NewRecorder()
		mount.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		return response.Code, response.Body.String()
	}

	It("lists no runs for a work dir that never traced one", func() {
		status, body := get(server.TracesMount)
		Expect(status).To(Equal(http.StatusOK))
		Expect(body).To(MatchJSON(`{"runs": []}`))
	})

	It("lists a recorded run and serves its store under its mount", func() {
		result, err := tracerun.Run(context.Background(), tracerun.Options{WorkDir: workDir, Command: []string{"true"}})
		Expect(err).ToNot(HaveOccurred())

		status, body := get(server.TracesMount)
		Expect(status).To(Equal(http.StatusOK), body)
		var listed server.TracesStatus
		Expect(json.Unmarshal([]byte(body), &listed)).To(Succeed())
		Expect(listed.Runs).To(Equal([]server.RunTraces{{RunID: result.RunID, Mount: server.TracesMount + "/" + result.RunID, Run: result}}))

		status, body = get(listed.Runs[0].Mount + "/api/v1/traces/" + result.Process.Tree.Kind + "/seq?stream=" + result.Process.Tree.Stream)
		Expect(status).To(Equal(http.StatusOK), body)
		var seq map[string]int64
		Expect(json.Unmarshal([]byte(body), &seq)).To(Succeed())
		Expect(seq["seq"]).To(BeNumerically(">", 0), "the final checkpoint committed the tree")
	})

	It("lists a run that left no manifest with why it is unavailable", func() {
		Expect(os.MkdirAll(filepath.Join(workDir, ".telltale", "traces", "run-crashed"), 0o755)).To(Succeed())
		_, body := get(server.TracesMount)
		Expect(body).To(MatchJSON(`{"runs": [{"runId": "run-crashed", "mount": "/api/traces/run-crashed", "unavailable": "the run did not finish: it left no run.json"}]}`))
	})

	It("answers a run it has no store for as not found", func() {
		status, body := get(server.TracesMount + "/run-missing/api/v1/traces/kinds")
		Expect(status).To(Equal(http.StatusNotFound))
		Expect(body).To(ContainSubstring("run-missing"))
	})
})
