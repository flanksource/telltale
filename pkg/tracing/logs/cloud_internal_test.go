package logs

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"time"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/models"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/commons-db/types"
	"github.com/gorilla/websocket"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type testCloudProvider struct{}

func (testCloudProvider) Type() string { return "test-log-cloud" }
func (testCloudProvider) Execute(_ dbcontext.Context, req query.ProviderRequest) ([]query.Row, error) {
	return []query.Row{{"message": req.Connection, "severity": "INFO"}}, nil
}
func (testCloudProvider) Stream(_ dbcontext.Context, _ query.ProviderRequest, emit func(query.Row)) error {
	emit(query.Row{"message": "live"})
	return nil
}

var _ = Describe("cloud log source", func() {
	It("filters cloud severity using the requested minimum", func() {
		var records []Log
		emit := func(row Log) error { records = append(records, row); return nil }
		Expect(emitCloud(query.Row{"message": "notice", "severity": "NOTICE"}, Params{MinLevel: "warn"}, emit)).To(Succeed())
		Expect(emitCloud(query.Row{"message": "warning", "severity": "WARNING"}, Params{MinLevel: "warn"}, emit)).To(Succeed())
		Expect(records).To(HaveLen(1))
		Expect(records[0].Message).To(Equal("warning"))
	})
	DescribeTable("refuses incomplete cloud configurations before searching",
		func(backend, connectionType, expected string) {
			GinkgoT().Setenv("GOOGLE_CLOUD_PROJECT", "")
			GinkgoT().Setenv("GCLOUD_PROJECT", "")
			GinkgoT().Setenv("GOOGLE_APPLICATION_CREDENTIALS", "/nonexistent/example-credentials.json")
			ctx := dbcontext.New().WithConnectionResolver(func(_ dbcontext.Context, _ string) (*models.Connection, error) {
				return &models.Connection{Name: "example", Type: connectionType}, nil
			})
			err := followCloud(ctx, Params{Backend: backend, Connection: "connection://example", Query: "example"}, func(Log) error { return nil })
			Expect(err).To(MatchError(ContainSubstring(expected)))
		},
		Entry("GCP Cloud Logging", "gcpcloudlogging", models.ConnectionTypeGCP, "project"),
		Entry("Azure Log Analytics", "azureloganalytics", models.ConnectionTypeAzure, "workspaceID"),
	)
	It("emits history and then stream rows through the selected connection", func() {
		query.RegisterProvider(testCloudProvider{})
		var records []Log
		err := followCloud(dbcontext.NewContext(context.Background()), Params{Backend: "test-log-cloud", Connection: "connection://example"}, func(row Log) error { records = append(records, row); return nil })
		Expect(err).NotTo(HaveOccurred())
		Expect(records).To(HaveLen(2))
		Expect(records[0].Message).To(Equal("connection://example"))
		Expect(records[1].Message).To(Equal("live"))
	})
	It("reads Loki history and follows live lines using the configured connection", func() {
		stop := make(chan struct{})
		upgrader := websocket.Upgrader{}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer GinkgoRecover()
			if r.URL.Path == "/loki/api/v1/tail" {
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer func() { Expect(conn.Close()).To(Succeed()) }()
				Expect(conn.WriteJSON(map[string]any{"streams": []any{map[string]any{"stream": map[string]string{"app": "api"}, "values": [][]string{{"1700000001000000000", "live"}}}}})).To(Succeed())
				<-stop
				return
			}
			_, _ = fmt.Fprint(w, `{"status":"success","data":{"resultType":"streams","result":[{"stream":{"app":"api"},"values":[["1700000000000000000","history"]]}]}}`)
		}))
		DeferCleanup(func() { close(stop); server.Close() })
		base, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		ctx := dbcontext.NewContext(base).WithConnectionResolver(func(_ dbcontext.Context, _ string) (*models.Connection, error) {
			return &models.Connection{Name: "loki", Type: models.ConnectionTypeLoki, URL: server.URL}, nil
		})
		records := make(chan Log, 2)
		done := make(chan error, 1)
		go func() {
			done <- followCloud(ctx, Params{Backend: "loki", Connection: "connection://loki", Query: `{app="api"}`}, func(row Log) error { records <- row; return nil })
		}()
		var first, second Log
		select {
		case first = <-records:
		case err := <-done:
			Fail(fmt.Sprintf("loki ended before history: %v", err))
		case <-time.After(10 * time.Second):
			Fail("loki did not emit history")
		}
		Eventually(records).WithTimeout(10 * time.Second).Should(Receive(&second))
		Expect([]string{first.Message, second.Message}).To(Equal([]string{"history", "live"}))
		cancel()
		Eventually(done).WithTimeout(10 * time.Second).Should(Receive(BeNil()))
	})
	It("reads CloudWatch history using the configured connection", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/x-amz-json-1.1")
			switch r.Header.Get("X-Amz-Target") {
			case "Logs_20140328.StartQuery":
				_, _ = fmt.Fprint(w, `{"queryId":"q-1"}`)
			case "Logs_20140328.GetQueryResults":
				_, _ = fmt.Fprint(w, `{"status":"Complete","statistics":{"recordsMatched":1},"results":[[{"field":"@timestamp","value":"2026-04-19 11:23:40.207"},{"field":"@message","value":"cloud history"}]]}`)
			default:
				http.Error(w, "unexpected AWS operation", http.StatusBadRequest)
			}
		}))
		DeferCleanup(server.Close)
		ctx := dbcontext.New().WithConnectionResolver(func(_ dbcontext.Context, _ string) (*models.Connection, error) {
			return &models.Connection{Name: "cloud", Type: models.ConnectionTypeAWS, Username: "example-access", Password: "example-secret", Properties: types.JSONStringMap{"region": "eu-west-1"}}, nil
		})
		var records []Log
		err := followCloud(ctx, Params{Backend: "cloudwatch", Connection: "connection://cloud", Query: "fields @message", Options: map[string]any{"logGroup": "example", "endpoint": server.URL}}, func(row Log) error { records = append(records, row); return nil })
		Expect(err).NotTo(HaveOccurred())
		Expect(records).To(HaveLen(1))
		Expect(records[0].Message).To(Equal("cloud history"))
		Expect(records[0].Resource).To(HaveKeyWithValue("cloud.provider", "aws"))
	})
	It("reads OpenSearch history and resumes its tail after the last document", func() {
		var searches atomic.Int32
		tailRequests := make(chan string, 4)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if strings.Contains(r.URL.Path, "point_in_time") {
				_, _ = fmt.Fprint(w, `{"pit_id":"pit-1","pits":[{"successful":true,"pit_id":"pit-1"}]}`)
				return
			}
			if !strings.HasSuffix(r.URL.Path, "/_search") {
				_, _ = fmt.Fprint(w, `{"version":{"number":"2.0.0"}}`)
				return
			}
			switch searches.Add(1) {
			case 1:
				_, _ = fmt.Fprint(w, `{"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_id":"1","_source":{"@timestamp":"2026-04-19T11:23:40Z","message":"history"},"sort":["2026-04-19T11:23:40Z","1"]}]}}`)
			case 2:
				body, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				tailRequests <- string(body)
				_, _ = fmt.Fprint(w, `{"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_id":"2","_source":{"@timestamp":"2026-04-19T11:23:41Z","message":"live"},"sort":["2026-04-19T11:23:41Z","2"]}]}}`)
			default:
				_, _ = fmt.Fprint(w, `{"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`)
			}
		}))
		DeferCleanup(server.Close)
		base, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		ctx := dbcontext.NewContext(base).WithConnectionResolver(func(_ dbcontext.Context, _ string) (*models.Connection, error) {
			return &models.Connection{Name: "search", Type: models.ConnectionTypeOpenSearch, URL: server.URL}, nil
		})
		records := make(chan Log, 4)
		done := make(chan error, 1)
		go func() {
			done <- followCloud(ctx, Params{Backend: "opensearch", Connection: "connection://search", Query: `{"query":{"match_all":{}}}`, TimeField: "@timestamp", Options: map[string]any{"index": "logs", "limit": "10"}}, func(row Log) error { records <- row; return nil })
		}()
		var first, second Log
		select {
		case first = <-records:
		case err := <-done:
			Fail(fmt.Sprintf("opensearch ended before history: %v", err))
		case <-time.After(10 * time.Second):
			Fail("opensearch did not emit history")
		}
		select {
		case second = <-records:
		case err := <-done:
			Fail(fmt.Sprintf("opensearch ended before live row: %v", err))
		case <-time.After(10 * time.Second):
			Fail(fmt.Sprintf("opensearch did not emit live row; first=%q searches=%d", first.Message, searches.Load()))
		}
		Expect([]string{first.Message, second.Message}).To(Equal([]string{"history", "live"}))
		var request string
		Eventually(tailRequests).Should(Receive(&request))
		Expect(request).To(ContainSubstring("search_after"))
		Expect(request).To(ContainSubstring("2026-04-19T11:23:40"))
		cancel()
		Eventually(done).WithTimeout(10 * time.Second).Should(Receive(BeNil()))
	})
})
