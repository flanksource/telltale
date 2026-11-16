package server_test

import (
	"net/http"
	"net/http/httptest"
	"testing/fstest"

	"github.com/flanksource/telltale/internal/server"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/spf13/cobra"
)

var _ = Describe("Profiles receiver routing", func() {
	It("dispatches Profiles exports to the configured OTLP receiver without serving the SPA", func() {
		var received string
		handler, err := server.NewHandler(server.HandlerOptions{
			Root: &cobra.Command{Use: "telltale"}, DataDir: GinkgoT().TempDir(),
			Assets: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("application")}},
			Traces: http.NotFoundHandler(), Top: http.NotFoundHandler(),
			OTLP: http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				received = request.URL.Path
				w.WriteHeader(http.StatusAccepted)
			}),
		})
		Expect(err).NotTo(HaveOccurred())
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/profiles", nil))
		Expect(response.Code).To(Equal(http.StatusAccepted))
		Expect(received).To(Equal("/v1/profiles"))
	})
})
