package server_test

import (
	"net/http"
	"net/http/httptest"
	"testing/fstest"

	"github.com/flanksource/telltale/internal/server"
	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/credentials/apitoken"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/spf13/cobra"
)

var _ = Describe("HTTP routing", func() {
	It("protects non-loopback API routes according to token scope", func() {
		dir := GinkgoT().TempDir()
		store := agentclient.NewTokenStore(dir)
		agent, err := store.Mint("agent", apitoken.ScopeAgent)
		Expect(err).NotTo(HaveOccurred())
		api, err := store.Mint("api", apitoken.ScopeAPI)
		Expect(err).NotTo(HaveOccurred())
		handler, err := server.NewHandler(server.HandlerOptions{Root: &cobra.Command{Use: "telltale"}, Assets: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}}, Traces: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }), Top: http.NotFoundHandler(), WorkDir: dir, RequireAPIAuth: true})
		Expect(err).NotTo(HaveOccurred())
		for _, tc := range []struct {
			token  string
			status int
		}{{"", 401}, {agent.Secret.Value(), 403}, {api.Secret.Value(), 204}} {
			req := httptest.NewRequest(http.MethodGet, "/api/traces", nil)
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			Expect(response.Code).To(Equal(tc.status))
			if tc.status != 204 {
				Expect(response.Header().Get("Content-Type")).To(ContainSubstring("application/json"))
			}
		}
	})
	It("keeps loopback API routes accessible without a token", func() {
		handler, err := server.NewHandler(server.HandlerOptions{Root: &cobra.Command{Use: "telltale"}, Assets: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}}, Traces: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }), Top: http.NotFoundHandler()})
		Expect(err).NotTo(HaveOccurred())
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/traces", nil))
		Expect(response.Code).To(Equal(204))
	})
	var handler http.Handler
	BeforeEach(func() {
		var err error
		handler, err = server.NewHandler(server.HandlerOptions{
			Root: &cobra.Command{Use: "telltale"}, Version: "test-version",
			Traces: http.NotFoundHandler(), Top: http.NotFoundHandler(),
			Assets: fstest.MapFS{
				"index.html":         &fstest.MapFile{Data: []byte("<html>Telltale</html>")},
				"assets/app-ab12.js": &fstest.MapFile{Data: []byte("export default 'telltale'")},
			},
		})
		Expect(err).NotTo(HaveOccurred())
	})

	DescribeTable("serves UI routes and rejects missing resources",
		func(method, path string, status int, cache string) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(method, path, nil))
			Expect(response.Code).To(Equal(status))
			Expect(response.Header().Get("Cache-Control")).To(Equal(cache))
		},
		Entry("index", "GET", "/", 200, "no-cache"),
		Entry("deep link", "GET", "/settings", 200, "no-cache"),
		Entry("a program's top page, its name dotted", "GET", "/top/com.apple.WebKit.Networking", 200, "no-cache"),
		Entry("a missing file beside the page", "GET", "/favicon.ico", 404, ""),
		Entry("hashed asset", "GET", "/assets/app-ab12.js", 200, "public, max-age=31536000, immutable"),
		Entry("missing asset", "GET", "/assets/missing.js", 404, ""),
		Entry("extensionless missing asset", "GET", "/assets/missing", 404, ""),
		Entry("missing API", "GET", "/api/missing", 404, ""),
		Entry("API root", "GET", "/api", 404, ""),
		Entry("unsupported UI method", "POST", "/settings", 405, ""),
	)

	It("serves Clicky's health and generated OpenAPI", func() {
		for _, path := range []string{"/health", "/api/openapi.json"} {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
			Expect(response.Code).To(Equal(200))
			Expect(response.Header().Get("Content-Type")).To(ContainSubstring("application/json"))
			Expect(response.Body.String()).To(ContainSubstring("test-version"))
		}
	})

	It("fails before startup when embedded assets are missing", func() {
		_, err := server.NewHandler(server.HandlerOptions{
			Root: &cobra.Command{Use: "telltale"}, Assets: fstest.MapFS{}, Version: "dev",
			Traces: http.NotFoundHandler(), Top: http.NotFoundHandler(),
		})
		Expect(err).To(MatchError(ContainSubstring("make build")))
	})
})
