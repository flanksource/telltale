package definition_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"

	"github.com/flanksource/commons-db/connection/store"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/datadir"
	"github.com/flanksource/telltale/pkg/definition"
)

const prefix = "/api/traces"

// probe is a host-only definition that reads SELECT 1 from an in-memory
// sqlite database, so a run of it reaches no network.
const probe = `{"apiVersion": "telltale.flanksource.com/v1alpha1", "kind": "TraceDefinition", "metadata": {"name": "probe"},
	"spec": {"description": "Reads one row", "collect": {"query": {"sql": [{"type": "sqlite", "url": ":memory:", "query": "SELECT 1 AS one"}]}}}}`

// scrapes is a definition named probe with two unnamed scrape entries.
const scrapes = `{"apiVersion": "telltale.flanksource.com/v1alpha1", "kind": "TraceDefinition", "metadata": {"name": "probe"},
	"spec": {"collect": {"prometheus": {"scrape": [{"endpoints": ["http://a:9100/metrics"]}, {"endpoints": ["http://b:9100/metrics"]}]}}}}`

type response struct {
	status int
	body   string
}

func (r response) json() map[string]any {
	var decoded map[string]any
	Expect(json.Unmarshal([]byte(r.body), &decoded)).To(Succeed(), r.body)
	return decoded
}

var _ = Describe("the definitions API", func() {
	var (
		handler *telltale.Handler
		dataDir string
	)

	BeforeEach(func() {
		dataDir = GinkgoT().TempDir()
		dirs := []datadir.ConfigDir{{Name: datadir.ConfigCWD, Dir: GinkgoT().TempDir()}, {Name: datadir.ConfigGlobal, Dir: GinkgoT().TempDir()}}
		connections, err := store.New(store.Layer{Name: store.LayerCWD, Dir: GinkgoT().TempDir()})
		Expect(err).ToNot(HaveOccurred())
		service, err := definition.NewService(definition.NewStore(dirs), connections)
		Expect(err).ToNot(HaveOccurred())
		handler, err = telltale.NewHandler(telltale.HandlerOptions{DataDir: dataDir, Prefix: prefix, Connections: connections, Definitions: service})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(handler.Close()).To(Succeed()) })
	})

	send := func(method, path, body string) response {
		request := httptest.NewRequest(method, prefix+path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return response{status: recorder.Code, body: recorder.Body.String()}
	}
	put := func(name, layer, resourceVersion, def string) response {
		return send(http.MethodPut, "/definitions/"+name, `{"layer": "`+layer+`", "resourceVersion": "`+resourceVersion+`", "definition": `+def+`}`)
	}
	created := func(def string) string {
		saved := put("probe", datadir.ConfigGlobal, "", def)
		Expect(saved.status).To(Equal(http.StatusOK), saved.body)
		return saved.json()["resourceVersion"].(string)
	}

	It("lists each definition with its layer and per-section counts, and gets one with its file and entries", func() {
		resourceVersion := created(scrapes)
		listed := send(http.MethodGet, "/definitions", "")
		Expect(listed.status).To(Equal(http.StatusOK), listed.body)
		var summaries []map[string]any
		Expect(json.Unmarshal([]byte(listed.body), &summaries)).To(Succeed())
		Expect(summaries).To(ContainElements(
			And(HaveKeyWithValue("name", "probe"), HaveKeyWithValue("layer", datadir.ConfigGlobal), HaveKeyWithValue("resourceVersion", resourceVersion),
				HaveKeyWithValue("counts", map[string]any{"proxy": 0.0, "instrument": 0.0, "collect": 2.0})),
			And(HaveKeyWithValue("name", "gops"), HaveKeyWithValue("layer", definition.LayerBuiltin)),
		))

		got := send(http.MethodGet, "/definitions/probe", "").json()
		Expect(got).To(And(HaveKeyWithValue("layer", datadir.ConfigGlobal), HaveKeyWithValue("resourceVersion", resourceVersion),
			HaveKeyWithValue("yaml", ContainSubstring("http://a:9100/metrics")), HaveKeyWithValue("definition", HaveKey("spec"))))
		Expect(got["entries"]).To(HaveExactElements(
			And(HaveKeyWithValue("pointer", "/spec/collect/prometheus/scrape/0"), HaveKeyWithValue("path", "collect.prometheus.scrape"),
				HaveKeyWithValue("kind", "prometheus"), HaveKeyWithValue("name", "probe-collect-prometheus-scrape-1"), HaveKeyWithValue("defaultName", "probe-collect-prometheus-scrape-1")),
			HaveKeyWithValue("name", "probe-collect-prometheus-scrape-2"),
		))
		Expect(send(http.MethodGet, "/definitions/missing", "").status).To(Equal(http.StatusNotFound))
	})

	It("serves the CRD schema, every typed list and the store's layers", func() {
		schema := send(http.MethodGet, "/definitions/schema", "").json()
		Expect(schema["schema"]).To(HaveKeyWithValue("properties", HaveKey("spec")))
		Expect(schema["lists"]).To(ContainElement(And(HaveKeyWithValue("path", "collect.prometheus.scrape"), HaveKeyWithValue("section", "collect"),
			HaveKeyWithValue("group", "prometheus"), HaveKeyWithValue("type", "scrape"), HaveKeyWithValue("kind", "prometheus"), HaveKeyWithValue("title", "Scrape"))))
		Expect(schema["layers"]).To(HaveExactElements(
			And(HaveKeyWithValue("name", datadir.ConfigCWD), HaveKeyWithValue("writable", true), HaveKeyWithValue("dir", Not(BeEmpty()))),
			And(HaveKeyWithValue("name", datadir.ConfigGlobal), HaveKeyWithValue("writable", true)),
			And(HaveKeyWithValue("name", definition.LayerBuiltin), HaveKeyWithValue("writable", false)),
		))
	})

	It("validates a definition statically, answering its errors beside each entry's default name", func() {
		invalid := strings.Replace(scrapes, `"collect"`, `"every": "soon", "collect"`, 1)
		validated := send(http.MethodPost, "/definitions/validate", `{"definition": `+invalid+`}`)
		Expect(validated.status).To(Equal(http.StatusOK), validated.body)
		Expect(validated.json()["errors"]).To(ConsistOf(HaveKeyWithValue("instancePath", "/spec/every")))
		Expect(validated.json()["entries"]).To(HaveExactElements(
			HaveKeyWithValue("defaultName", "probe-collect-prometheus-scrape-1"), HaveKeyWithValue("defaultName", "probe-collect-prometheus-scrape-2"),
		))
		Expect(send(http.MethodPost, "/definitions/validate", `{"definition": {"spec": 1}}`).status).To(Equal(http.StatusBadRequest))
	})

	It("creates, replaces only at the version read, and refuses an invalid or built-in definition", func() {
		first := created(scrapes)
		exists := put("probe", datadir.ConfigGlobal, "", scrapes)
		Expect(exists.status).To(Equal(http.StatusConflict))
		Expect(exists.json()).To(HaveKeyWithValue("resourceVersion", first))

		replaced := put("probe", datadir.ConfigGlobal, first, probe)
		Expect(replaced.status).To(Equal(http.StatusOK), replaced.body)
		second := replaced.json()["resourceVersion"].(string)
		Expect(second).ToNot(Equal(first))
		stale := put("probe", datadir.ConfigGlobal, first, scrapes)
		Expect(stale.status).To(Equal(http.StatusConflict))
		Expect(stale.json()).To(And(HaveKeyWithValue("resourceVersion", second), HaveKeyWithValue("error", ContainSubstring("changed since it was read"))))

		renamed := put("other", datadir.ConfigGlobal, "", probe)
		Expect(renamed.status).To(Equal(http.StatusBadRequest))
		Expect(renamed.json()["errors"]).To(ConsistOf(HaveKeyWithValue("instancePath", "/metadata/name")))
		invalid := put("probe", datadir.ConfigGlobal, second, strings.Replace(probe, `"description"`, `"every": "soon", "description"`, 1))
		Expect(invalid.status).To(Equal(http.StatusBadRequest))
		Expect(invalid.json()).To(And(HaveKeyWithValue("name", "probe"), HaveKeyWithValue("errors", ConsistOf(HaveKeyWithValue("instancePath", "/spec/every")))))
		builtin := put("gops", definition.LayerBuiltin, "", strings.Replace(probe, `"probe"`, `"gops"`, 1))
		Expect(builtin.status).To(Equal(http.StatusBadRequest))
		Expect(builtin.body).To(ContainSubstring("read-only"))
	})

	It("deletes only at the version read", func() {
		resourceVersion := created(probe)
		remove := func(version string) int {
			return send(http.MethodDelete, "/definitions/probe?"+url.Values{"layer": {datadir.ConfigGlobal}, "resourceVersion": {version}}.Encode(), "").status
		}
		Expect(remove("stale")).To(Equal(http.StatusConflict))
		Expect(remove(resourceVersion)).To(Equal(http.StatusNoContent))
		Expect(remove(resourceVersion)).To(Equal(http.StatusNotFound))
	})

	It("starts a run of a stored definition and records the definition on it", func() {
		resourceVersion := created(probe)
		started := send(http.MethodPost, "", `{"definition": "probe", "duration": "500ms"}`)
		Expect(started.status).To(Equal(http.StatusCreated), started.body)
		runID := started.json()["runId"].(string)
		var finished telltale.RunInfo
		Eventually(func() telltale.Status {
			var err error
			finished, err = telltale.Get(telltale.GetOptions{DataDir: dataDir, RunID: runID})
			Expect(err).ToNot(HaveOccurred())
			return finished.Status
		}, 15*time.Second, 100*time.Millisecond).ShouldNot(Equal(telltale.StatusRunning))
		Expect(finished.Captures).To(ConsistOf(HaveField("Kind", "query")))
		Expect(finished.Definition).To(HaveValue(And(HaveField("Name", "probe"), HaveField("ResourceVersion", resourceVersion))))
		Expect(finished.Definition.Spec).To(MatchJSON(`{"description": "Reads one row", "collect": {"query": {"sql": [
			{"name": "probe-collect-query-sql", "type": "sqlite", "url": ":memory:", "query": "SELECT 1 AS one"}]}}}`))
	})

	It("refuses a start that names a definition and ad-hoc traces, or a definition it does not have", func() {
		both := send(http.MethodPost, "", `{"definition": "probe", "command": "true", "traces": {"http": {"enabled": true}}}`)
		Expect(both.status).To(Equal(http.StatusBadRequest))
		Expect(both.json()["errors"]).To(ConsistOf(HaveKeyWithValue("instancePath", "/definition")))
		missing := send(http.MethodPost, "", `{"definition": "missing", "command": "true"}`)
		Expect(missing.status).To(Equal(http.StatusBadRequest))
		Expect(missing.json()["errors"]).To(ConsistOf(HaveKeyWithValue("instancePath", "/definition")))
	})
})
