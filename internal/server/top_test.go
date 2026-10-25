package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"

	"github.com/flanksource/telltale/internal/server"
	"github.com/flanksource/telltale/pkg/tracing/groups"
	"github.com/flanksource/telltale/pkg/tracing/process"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the live process groups", func() {
	var top *server.TopHandler

	BeforeEach(func() {
		top = server.NewTopHandler(GinkgoT().TempDir())
		DeferCleanup(top.Close)
	})

	get := func(method, target string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		top.ServeHTTP(response, httptest.NewRequest(method, target, nil))
		return response
	}

	It("serves the host's latest frame, every program's group included with all=true", func() {
		response := get(http.MethodGet, server.TopMount+"?all=true")

		Expect(response.Code).To(Equal(http.StatusOK), response.Body.String())
		Expect(response.Header().Get("Cache-Control")).To(Equal("no-store"))
		var frame groups.Frame
		Expect(json.Unmarshal(response.Body.Bytes(), &frame)).To(Succeed())
		Expect(frame.Tick).To(BeNumerically(">=", 1))
		Expect(frame.Groups).To(ContainElement(HaveField("Kind", groups.KindOther)))
	})

	It("serves the processes running a program, and a process with those under it, with the host's use", func() {
		var byName, byPID server.TopProcesses
		self := filepath.Base(os.Args[0])
		response := get(http.MethodGet, server.TopProcessesPath+"?all=true&name="+url.QueryEscape(self))
		Expect(response.Code).To(Equal(http.StatusOK), response.Body.String())
		Expect(json.Unmarshal(response.Body.Bytes(), &byName)).To(Succeed())
		Expect(byName.Host).NotTo(BeNil())
		Expect(byName.Processes).To(ContainElement(HaveField("PID", os.Getpid())))
		Expect(byName.Processes).To(HaveEach(HaveField("Name", self)))

		response = get(http.MethodGet, server.TopProcessesPath+"?pid=1&all=true")
		Expect(response.Code).To(Equal(http.StatusOK), response.Body.String())
		Expect(json.Unmarshal(response.Body.Bytes(), &byPID)).To(Succeed())
		Expect(byPID.Processes).To(ContainElement(HaveField("PID", 1)))
		Expect(len(byPID.Processes)).To(BeNumerically(">", 1), "pid 1's subtree is every process")
	})

	It("serves a listed process's facets, its open files among them", func() {
		response := get(http.MethodGet, server.TopFacetsPath+"?all=true&pid="+strconv.Itoa(os.Getpid()))

		Expect(response.Code).To(Equal(http.StatusOK), response.Body.String())
		var facets groups.Facets
		Expect(json.Unmarshal(response.Body.Bytes(), &facets)).To(Succeed())
		Expect(facets.PID).To(Equal(os.Getpid()))
		Expect(facets.Descriptors).To(HaveKeyWithValue(process.FacetOpenFiles, Not(BeEmpty())))
		Expect(facets.Environment).NotTo(BeEmpty())
	})

	DescribeTable("refuses what it cannot serve",
		func(method, target string, status int, says string) {
			response := get(method, target)
			Expect(response.Code).To(Equal(status))
			Expect(response.Body.String()).To(ContainSubstring(says))
		},
		Entry("a flag that is no boolean", http.MethodGet, server.TopMount+"?all=maybe", http.StatusBadRequest, `all="maybe"`),
		Entry("a write", http.MethodPost, server.TopMount, http.StatusMethodNotAllowed, "method not allowed"),
		Entry("processes of neither a program nor a process", http.MethodGet, server.TopProcessesPath, http.StatusBadRequest, "one of them"),
		Entry("processes of both", http.MethodGet, server.TopProcessesPath+"?name=go&pid=1", http.StatusBadRequest, "one of them"),
		Entry("a pid that is no process id", http.MethodGet, server.TopProcessesPath+"?pid=x", http.StatusBadRequest, `pid="x"`),
		Entry("facets of no process", http.MethodGet, server.TopFacetsPath, http.StatusBadRequest, `pid=""`),
		Entry("facets of a process the latest frame did not list", http.MethodGet, server.TopFacetsPath+"?pid=4194304", http.StatusNotFound, "not listed"),
	)
})
