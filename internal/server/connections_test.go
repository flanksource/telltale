package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing/fstest"

	"github.com/flanksource/commons-db/connection/store"
	"github.com/flanksource/telltale/internal/server"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/spf13/cobra"
)

var _ = Describe("Connection workspace API", func() {
	It("serves the shared schema, persists CRUD and queries a workspace connection", func() {
		dir := GinkgoT().TempDir()
		connections, err := store.New(store.Layer{Name: store.LayerCWD, Dir: filepath.Join(dir, ".connections")})
		Expect(err).NotTo(HaveOccurred())
		handler, err := server.NewHandler(server.HandlerOptions{
			Root: &cobra.Command{Use: "telltale"}, Connections: connections,
			Assets: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}},
			Traces: http.NotFoundHandler(), Top: http.NotFoundHandler(),
		})
		Expect(err).NotTo(HaveOccurred())
		request := func(method, path, body string) *httptest.ResponseRecorder {
			response := httptest.NewRecorder()
			req := httptest.NewRequest(method, path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			handler.ServeHTTP(response, req)
			return response
		}
		schema := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/v1/connection", nil)
		req.Header.Set("Accept", "application/schema+json")
		handler.ServeHTTP(schema, req)
		Expect(schema.Code).To(Equal(200))
		Expect(schema.Body.String()).To(ContainSubstring("properties"))
		for _, body := range []string{`null`, `{}`, `{"name":"local"}`, `{} {}`} {
			Expect(request("POST", "/api/v1/connection", body).Code).To(Equal(400))
		}
		crossOrigin := httptest.NewRequest("POST", "/api/v1/connection", strings.NewReader(`{"name":"foreign","type":"sqlite"}`))
		crossOrigin.Header.Set("Sec-Fetch-Site", "cross-site")
		refused := httptest.NewRecorder()
		handler.ServeHTTP(refused, crossOrigin)
		Expect(refused.Code).To(Equal(403))
		created := request("POST", "/api/v1/connection", `{"name":"local","type":"sqlite","url":"`+filepath.Join(dir, "local.db")+`"}`)
		Expect(created.Code).To(Equal(201), created.Body.String())
		var connection struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal(created.Body.Bytes(), &connection)).To(Succeed())
		path := "/api/v1/connection/" + connection.ID
		Expect(request("GET", path, "").Body.String()).To(ContainSubstring(`"name":"local"`))
		Expect(request("GET", "/api/v1/connections/dashboard", "").Body.String()).To(ContainSubstring(`"name":"local"`))
		Expect(request("GET", path+"/browser", "").Code).To(Equal(200))
		queried := request("POST", path+"/browser/query", `{"query":"SELECT 7 AS value","options":{}}`)
		Expect(queried.Code).To(Equal(200), queried.Body.String())
		Expect(queried.Body.String()).To(ContainSubstring(`"value":7`))
		updated := request("PUT", path, `{"name":"renamed","type":"sqlite","url":"`+filepath.Join(dir, "local.db")+`"}`)
		Expect(updated.Code).To(Equal(200), updated.Body.String())
		Expect(json.Unmarshal(updated.Body.Bytes(), &connection)).To(Succeed())
		Expect(request("DELETE", "/api/v1/connection/"+connection.ID, "").Code).To(Equal(204))
		Expect(request("GET", "/api/v1/connection/"+connection.ID, "").Code).To(Equal(404))
	})
})
