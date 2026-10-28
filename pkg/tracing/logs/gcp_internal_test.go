package logs

import (
	"context"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"cloud.google.com/go/logging/apiv2/loggingpb"
	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/models"
	"github.com/flanksource/commons-db/types"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type loggingStub struct {
	loggingpb.UnimplementedLoggingServiceV2Server
}

func (loggingStub) ListLogEntries(context.Context, *loggingpb.ListLogEntriesRequest) (*loggingpb.ListLogEntriesResponse, error) {
	return &loggingpb.ListLogEntriesResponse{Entries: []*loggingpb.LogEntry{{Timestamp: timestamppb.Now(), Payload: &loggingpb.LogEntry_TextPayload{TextPayload: "gcp history"}}}}, nil
}

var _ = Describe("GCP Cloud Logging source", func() {
	It("reads log entries using a configured connection", func() {
		token := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"access_token":"example-token","token_type":"Bearer","expires_in":3600}`)
		}))
		DeferCleanup(token.Close)
		service := grpc.NewServer()
		loggingpb.RegisterLoggingServiceV2Server(service, loggingStub{})
		server := httptest.NewUnstartedServer(service)
		server.EnableHTTP2 = true
		server.StartTLS()
		DeferCleanup(server.Close)
		cert := filepath.Join(GinkgoT().TempDir(), "root.pem")
		Expect(os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0o600)).To(Succeed())
		GinkgoT().Setenv("SSL_CERT_FILE", cert)
		credentials := fmt.Sprintf(`{"type":"authorized_user","client_id":"example","client_secret":"example","refresh_token":"example","token_uri":%q}`, token.URL)
		ctx := dbcontext.New().WithConnectionResolver(func(_ dbcontext.Context, _ string) (*models.Connection, error) {
			return &models.Connection{Name: "gcp", Type: models.ConnectionTypeGCP, URL: server.Listener.Addr().String(), Certificate: credentials, Properties: types.JSONStringMap{"project": "example"}}, nil
		})
		var records []Log
		err := followCloud(ctx, Params{Backend: "gcpcloudlogging", Connection: "connection://gcp"}, func(row Log) error { records = append(records, row); return nil })
		Expect(err).NotTo(HaveOccurred())
		Expect(records).To(HaveLen(1))
		Expect(records[0].Message).To(Equal("gcp history"))
		Expect(records[0].Resource).To(HaveKeyWithValue("cloud.provider", "gcp"))
	})
})
