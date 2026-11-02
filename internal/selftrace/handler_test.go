package selftrace

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/websocket"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestSelfTrace(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "HTTP self-tracing")
}

func middleware() (*Tracer, *tracetest.InMemoryExporter) {
	GinkgoHelper()
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter), sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())))
	DeferCleanup(func() { Expect(provider.Shutdown(context.Background())).To(Succeed()) })
	return &Tracer{provider: provider}, exporter
}

var _ = Describe("HTTP middleware", func() {
	It("records the matched route, status and incoming parent without changing globals or capturing secrets", func() {
		tracer, exporter := middleware()
		global := otel.GetTracerProvider()
		mux := http.NewServeMux()
		mux.HandleFunc("GET /orders/{id}", func(w http.ResponseWriter, r *http.Request) {
			Expect(otel.GetTracerProvider()).To(BeIdenticalTo(global))
			w.Header().Set("Authorization", "private-response-value")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, "private-response-body")
		})
		request := httptest.NewRequest(http.MethodGet, "/orders/example?password=private-query-value", nil)
		request.Header.Set("traceparent", "00-1234567890abcdef1234567890abcdef-1234567890abcdef-01")
		request.Header.Set("Authorization", "private-request-value")
		response := httptest.NewRecorder()
		tracer.Handler(mux).ServeHTTP(response, request)
		Expect(response.Code).To(Equal(http.StatusInternalServerError))
		spans := exporter.GetSpans()
		Expect(spans).To(HaveLen(1))
		Expect(spans[0].Name).To(Equal("GET /orders/{id}"))
		Expect(spans[0].SpanContext.TraceID().String()).To(Equal("1234567890abcdef1234567890abcdef"))
		Expect(spans[0].Parent.SpanID().String()).To(Equal("1234567890abcdef"))
		Expect(spans[0].Status.Code).To(Equal(codes.Error))
		Expect(spans[0].Attributes).To(ContainElement(attribute.Int("http.response.status_code", http.StatusInternalServerError)))
		Expect(fmt.Sprint(spans[0].Attributes)).NotTo(ContainSubstring("private-"))
		Expect(spans[0].EndTime.Before(spans[0].StartTime)).To(BeFalse())
	})
	It("excludes OTLP ingestion and respects an unsampled remote parent", func() {
		tracer, exporter := middleware()
		handler := tracer.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
		for _, path := range []string{"/v1/traces", "/v1/traces/", "/health"} {
			request := httptest.NewRequest(http.MethodPost, path, nil)
			request.Header.Set("traceparent", "00-1234567890abcdef1234567890abcdef-1234567890abcdef-00")
			if path != "/health" {
				request.Header.Del("traceparent")
			}
			handler.ServeHTTP(httptest.NewRecorder(), request)
		}
		Expect(exporter.GetSpans()).To(BeEmpty())
	})
	It("preserves response flushing and connection hijacking", func() {
		tracer, exporter := middleware()
		mux := http.NewServeMux()
		mux.HandleFunc("GET /stream", func(w http.ResponseWriter, _ *http.Request) {
			defer GinkgoRecover()
			_, _ = io.WriteString(w, "streamed\n")
			Expect(http.NewResponseController(w).Flush()).To(Succeed())
		})
		upgrader := websocket.Upgrader{}
		mux.HandleFunc("GET /socket", func(w http.ResponseWriter, r *http.Request) {
			defer GinkgoRecover()
			connection, err := upgrader.Upgrade(w, r, nil)
			Expect(err).NotTo(HaveOccurred())
			defer func() { Expect(connection.Close()).To(Succeed()) }()
			Expect(connection.WriteMessage(websocket.TextMessage, []byte("connected"))).To(Succeed())
		})
		server := httptest.NewServer(tracer.Handler(mux))
		DeferCleanup(server.Close)
		response, err := http.Get(server.URL + "/stream")
		Expect(err).NotTo(HaveOccurred())
		body, err := io.ReadAll(response.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(response.Body.Close()).To(Succeed())
		Expect(string(body)).To(Equal("streamed\n"))
		connection, _, err := websocket.DefaultDialer.Dial("ws"+server.URL[4:]+"/socket", nil)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(connection.Close)
		_, message, err := connection.ReadMessage()
		Expect(err).NotTo(HaveOccurred())
		Expect(string(message)).To(Equal("connected"))
		Eventually(func() int { return len(exporter.GetSpans()) }).Should(Equal(2))
	})
})
