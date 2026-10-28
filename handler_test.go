package telltale_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/flanksource/commons-db/connection/store"
	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/tracing/httpproxy"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const prefix = "/api/traces"

var _ = Describe("the runs handler", func() {
	var handler *telltale.Handler
	var workDir, connectionsDir string
	var connections *store.Store

	BeforeEach(func() {
		workDir, connectionsDir = GinkgoT().TempDir(), GinkgoT().TempDir()
		var err error
		connections, err = store.New(store.Layer{Name: store.LayerCWD, Dir: connectionsDir})
		Expect(err).ToNot(HaveOccurred())
		handler, err = telltale.NewHandler(telltale.HandlerOptions{WorkDir: workDir, Prefix: prefix, Connections: connections})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(handler.Close()).To(Succeed()) })
	})

	serve := func(request *http.Request) (int, string) {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response.Code, response.Body.String()
	}
	get := func(path string) (int, string) { return serve(httptest.NewRequest(http.MethodGet, path, nil)) }
	post := func(body string, header http.Header) (int, string) {
		request := httptest.NewRequest(http.MethodPost, prefix, strings.NewReader(body))
		request.Header = header
		return serve(request)
	}
	jsonHeader := func() http.Header { return http.Header{"Content-Type": {"application/json"}} }
	list := func(query string) telltale.RunsList {
		status, body := get(prefix + query)
		Expect(status).To(Equal(http.StatusOK), body)
		var runs telltale.RunsList
		Expect(json.Unmarshal([]byte(body), &runs)).To(Succeed())
		return runs
	}

	It("refuses a mount without a work dir, a rooted prefix or a connection store", func() {
		_, err := telltale.NewHandler(telltale.HandlerOptions{WorkDir: workDir, Prefix: "api/traces/", Connections: connections})
		Expect(err).To(MatchError(ContainSubstring("prefix like /api/traces")))
		_, err = telltale.NewHandler(telltale.HandlerOptions{WorkDir: workDir, Prefix: prefix})
		Expect(err).To(MatchError(ContainSubstring("a connection store is required")))
	})

	It("lists the connections its runs can name, by type, without their URLs or credentials", func() {
		for name, spec := range map[string]string{"orders": "mssql: {url: {value: 'sqlserver://u:pw@db'}}", "logs": "loki: {url: 'http://loki'}"} {
			Expect(os.WriteFile(filepath.Join(connectionsDir, name+".yaml"), []byte(
				"apiVersion: mission-control.flanksource.com/v1\nkind: Connection\nmetadata: {namespace: shop}\nspec: {"+spec+"}\n",
			), 0o600)).To(Succeed())
		}
		status, body := get(prefix + "/connections?type=sql_server")
		Expect(status).To(Equal(http.StatusOK), body)
		Expect(body).To(MatchJSON(`{"connections": [{"reference": "connection://shop/orders", "name": "orders", "namespace": "shop", "type": "sql_server", "source": "file://` +
			filepath.Join(connectionsDir, "orders.yaml") + `"}]}`))

		info, err := telltale.GetConnection(connections, "orders")
		Expect(err).ToNot(HaveOccurred())
		Expect(info.Reference).To(Equal("connection://shop/orders"))
		_, err = telltale.GetConnection(connections, "missing")
		Expect(err).To(MatchError(os.ErrNotExist))
	})

	It("lists no runs for a work dir that never traced one", func() {
		status, body := get(prefix)
		Expect(status).To(Equal(http.StatusOK))
		Expect(body).To(MatchJSON(`{"runs": []}`))
	})

	It("lists a recorded run at its mount and serves its store there", func() {
		run, err := telltale.Start(context.Background(), telltale.StartOptions{WorkDir: workDir, Command: []string{"true"}})
		Expect(err).ToNot(HaveOccurred())
		result, err := run.Wait()
		Expect(err).ToNot(HaveOccurred())

		runs := list("").Runs
		Expect(runs).To(HaveLen(1))
		Expect(runs[0].Mount).To(Equal(prefix + "/" + result.RunID))
		Expect(runs[0].Status).To(Equal(telltale.StatusSucceeded))

		status, body := get(runs[0].Mount + "/api/v1/traces/" + result.Process.Tree.Kind + "/seq?stream=" + result.Process.Tree.Stream)
		Expect(status).To(Equal(http.StatusOK), body)
		var seq map[string]int64
		Expect(json.Unmarshal([]byte(body), &seq)).To(Succeed())
		Expect(seq["seq"]).To(BeNumerically(">", 0), "the final checkpoint committed the tree")
	})

	It("lists a run that left no manifest with why it is unavailable", func() {
		Expect(os.MkdirAll(filepath.Join(workDir, ".telltale", "traces", "run-crashed"), 0o755)).To(Succeed())
		_, body := get(prefix)
		Expect(body).To(MatchJSON(`{"runs": [{"runId": "run-crashed", "dir": "` + filepath.Join(workDir, ".telltale", "traces", "run-crashed") +
			`", "status": "unfinished", "mount": "/api/traces/run-crashed", "unavailable": "the run did not finish: it left no run.json"}]}`))
	})

	DescribeTable("refuses a filter it cannot read",
		func(query, expected string) {
			status, body := get(prefix + query)
			Expect(status).To(Equal(http.StatusBadRequest))
			Expect(body).To(ContainSubstring(expected))
		},
		Entry("an unknown status", "?status=lost", `unknown run status "lost"`),
		Entry("a start that is not a time", "?since=yesterday", "RFC 3339"),
		Entry("a negative limit", "?limit=-1", "want a count of runs"),
	)

	It("answers a run it has no store for as not found", func() {
		status, body := get(prefix + "/run-missing/api/v1/traces/kinds")
		Expect(status).To(Equal(http.StatusNotFound))
		Expect(body).To(ContainSubstring("run-missing"))
	})

	It("serves the start form", func() {
		status, body := get(prefix + "/start")
		Expect(status).To(Equal(http.StatusOK), body)
		var form map[string]map[string]any
		Expect(json.Unmarshal([]byte(body), &form)).To(Succeed())
		Expect(form["schema"]).To(HaveKeyWithValue("required", []any{"command"}))
		Expect(form["defaults"]).To(HaveKeyWithValue("traces", HaveKey(httpproxy.Kind)))
	})

	DescribeTable("refuses a start that is not a JSON body from its own origin",
		func(header http.Header) {
			status, _ := post(`{"command":"true"}`, header)
			Expect(status).To(Equal(http.StatusForbidden))
		},
		Entry("a form post", http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}),
		Entry("another origin", http.Header{"Content-Type": {"application/json"}, "Origin": {"https://elsewhere.example"}}),
	)

	It("answers a refused start request with each field's error", func() {
		status, body := post(`{"command":"","traces":{"http":{"enabled":true,"target":{"seam":"proxy"},"mode":"sideways"}}}`, jsonHeader())
		Expect(status).To(Equal(http.StatusBadRequest))
		var refused telltale.ValidationError
		Expect(json.Unmarshal([]byte(body), &refused)).To(Succeed())
		Expect(refused.Fields).To(ConsistOf(
			HaveField("InstancePath", "/command"),
			HaveField("InstancePath", "/traces/http"),
		))
	})

	It("starts a run, serves it live from its captures, and stops it", func() {
		status, body := post(`{"command":"sleep 30","traces":{"http":{"enabled":true,"target":{"seam":"proxy"}}}}`, jsonHeader())
		Expect(status).To(Equal(http.StatusCreated), body)
		var started telltale.MountedRun
		Expect(json.Unmarshal([]byte(body), &started)).To(Succeed())
		Expect(started.Status).To(Equal(telltale.StatusRunning))
		Expect(started.Live).ToNot(BeNil())
		Expect(started.Captures).To(ConsistOf(HaveField("Kind", httpproxy.Kind)))

		Expect(list("?status=running").Runs).To(ConsistOf(HaveField("RunInfo.RunID", started.RunID)))
		status, body = get(started.Mount + "/api/v1/traces/kinds")
		Expect(status).To(Equal(http.StatusOK), body)

		status, _ = serve(httptest.NewRequest(http.MethodDelete, started.Mount, nil))
		Expect(status).To(Equal(http.StatusAccepted))
		Eventually(func() telltale.Status {
			runs := list("?q=sleep").Runs
			Expect(runs).To(HaveLen(1))
			return runs[0].Status
		}, 15*time.Second, 100*time.Millisecond).Should(Equal(telltale.StatusFailed))

		status, _ = serve(httptest.NewRequest(http.MethodDelete, started.Mount, nil))
		Expect(status).To(Equal(http.StatusConflict))
	})

	It("stops the runs it started when it is closed", func() {
		status, body := post(`{"command":"sleep 30"}`, jsonHeader())
		Expect(status).To(Equal(http.StatusCreated), body)
		Expect(handler.Close()).To(Succeed())
		runs, err := telltale.List(telltale.ListOptions{WorkDir: workDir})
		Expect(err).ToNot(HaveOccurred())
		Expect(runs).To(ConsistOf(HaveField("Status", telltale.StatusFailed)))
	})
})
