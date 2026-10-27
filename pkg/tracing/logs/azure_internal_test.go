package logs

import (
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/models"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Azure Log Analytics source", func() {
	It("reads history using the configured connection endpoint", func() {
		identity := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"access_token":"example-token","token_type":"Bearer","expires_in":3600,"expires_on":"2000000000"}`)
		}))
		DeferCleanup(identity.Close)
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"tables":[{"name":"PrimaryResult","columns":[{"name":"Message","type":"string"}],"rows":[["azure history"]]}]}`)
		}))
		DeferCleanup(server.Close)
		cert := filepath.Join(GinkgoT().TempDir(), "root.pem")
		Expect(os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0o600)).To(Succeed())
		GinkgoT().Setenv("SSL_CERT_FILE", cert)
		GinkgoT().Setenv("AZURE_CLIENT_ID", "example")
		GinkgoT().Setenv("AZURE_TENANT_ID", "")
		GinkgoT().Setenv("AZURE_CLIENT_SECRET", "")
		GinkgoT().Setenv("IDENTITY_ENDPOINT", identity.URL)
		GinkgoT().Setenv("IDENTITY_HEADER", "example")
		GinkgoT().Setenv("HTTPS_PROXY", identity.URL)
		GinkgoT().Setenv("PATH", GinkgoT().TempDir())
		ctx := dbcontext.New().WithConnectionResolver(func(_ dbcontext.Context, _ string) (*models.Connection, error) {
			return &models.Connection{Name: "azure", Type: models.ConnectionTypeAzure, URL: server.URL}, nil
		})
		var records []Log
		err := followCloud(ctx, Params{Backend: "azureloganalytics", Connection: "connection://azure", Query: "Example | take 1", Options: map[string]any{"workspaceID": "example", "start": "now-1h"}}, func(row Log) error { records = append(records, row); return nil })
		Expect(err).NotTo(HaveOccurred())
		Expect(records).To(HaveLen(1))
		Expect(records[0].Message).To(Equal("azure history"))
		Expect(records[0].Resource).To(HaveKeyWithValue("cloud.provider", "azure"))
	})
})
