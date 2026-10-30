package otelreceiver

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"

	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/credentials/apitoken"
	"github.com/flanksource/telltale/pkg/datadir"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
)

func testReceiver(options Options) (*Receiver, *Store) {
	GinkgoHelper()
	store, err := OpenStore(StoreOptions{DataDir: GinkgoT().TempDir(), Retention: options.Retention, Writable: true})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(store.Close)
	receiver, err := New(Config{Store: store, Options: options, Verifier: apitoken.NewVerifier(agentclient.NewTokenStore([]datadir.ConfigDir{{Name: datadir.ConfigGlobal, Dir: store.options.DataDir}}).Lookup)})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { Expect(receiver.Stop(context.Background())).To(Succeed()) })
	return receiver, store
}

var _ = Describe("OTLP HTTP receiver", func() {
	DescribeTable("validates envelopes without persisting spans", func(method, format, encoding, body string, code int) {
		receiver, store := testReceiver(DefaultOptions())
		post := httptest.NewRequest(method, "/v1/traces", bytes.NewBufferString(body))
		post.Header.Set("Content-Type", format)
		post.Header.Set("Content-Encoding", encoding)
		response := httptest.NewRecorder()
		receiver.HTTPHandler(false).ServeHTTP(response, post)
		Expect(response.Code).To(Equal(code), response.Body.String())
		spans, err := store.Read(context.Background(), ReadOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(spans).To(BeEmpty())
	},
		Entry("method", http.MethodGet, "", "", "", http.StatusMethodNotAllowed),
		Entry("JSON", http.MethodPost, jsonType, "", "malformed", http.StatusBadRequest),
		Entry("protobuf", http.MethodPost, protobufType, "", "\xff", http.StatusBadRequest),
		Entry("gzip", http.MethodPost, jsonType, "gzip", "malformed", http.StatusBadRequest),
		Entry("encoding", http.MethodPost, jsonType, "unknown", "{}", http.StatusBadRequest),
		Entry("unknown JSON fields", http.MethodPost, jsonType, "", `{"resourceSpans":[],"futureField":true}`, http.StatusOK),
	)
	DescribeTable("commits an export before acknowledging it", func(contentType string, compressed bool) {
		receiver, store := testReceiver(DefaultOptions())
		request := exampleRequest()
		var body []byte
		var err error
		if contentType == "application/json" {
			body, err = request.MarshalJSON()
		} else {
			body, err = request.MarshalProto()
		}
		Expect(err).NotTo(HaveOccurred())
		if compressed {
			var buffer bytes.Buffer
			writer := gzip.NewWriter(&buffer)
			_, err = writer.Write(body)
			Expect(err).NotTo(HaveOccurred())
			Expect(writer.Close()).To(Succeed())
			body = buffer.Bytes()
		}
		post := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(body))
		post.Header.Set("Content-Type", contentType)
		if compressed {
			post.Header.Set("Content-Encoding", "gzip")
		}
		response := httptest.NewRecorder()
		receiver.HTTPHandler(false).ServeHTTP(response, post)
		Expect(response.Code).To(Equal(http.StatusOK), response.Body.String())
		Expect(response.Header().Get("Content-Type")).To(Equal(contentType))
		stored, err := store.Read(context.Background(), ReadOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(stored).To(HaveLen(1))
	}, Entry("protobuf", "application/x-protobuf", false), Entry("OTLP JSON", "application/json", false), Entry("gzip", "application/json", true))
	It("returns partial success for invalid spans and a retryable failure for unavailable storage", func() {
		receiver, store := testReceiver(DefaultOptions())
		request := exampleRequest()
		request.Traces().ResourceSpans().At(0).ScopeSpans().At(0).Spans().AppendEmpty()
		body, err := request.MarshalJSON()
		Expect(err).NotTo(HaveOccurred())
		server := httptest.NewServer(receiver.HTTPHandler(false))
		DeferCleanup(server.Close)
		response, err := http.Post(server.URL+"/v1/traces", "application/json", bytes.NewReader(body))
		Expect(err).NotTo(HaveOccurred())
		answer, err := io.ReadAll(response.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(response.Body.Close()).To(Succeed())
		decoded := ptraceotlp.NewExportResponse()
		Expect(decoded.UnmarshalJSON(answer)).To(Succeed())
		Expect(decoded.PartialSuccess().RejectedSpans()).To(Equal(int64(1)))
		Expect(store.Close()).To(Succeed())
		response, err = http.Post(server.URL+"/v1/traces", "application/json", bytes.NewReader(body))
		Expect(err).NotTo(HaveOccurred())
		Expect(response.Body.Close()).To(Succeed())
		Expect(response.StatusCode).To(Equal(http.StatusServiceUnavailable))
	})
})
