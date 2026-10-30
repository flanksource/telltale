package otelreceiver

import (
	"context"
	"net"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/flanksource/clicky/task"
	cctx "github.com/flanksource/commons/context"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
)

func serveGRPC(receiver *Receiver, requireAuth bool) string {
	GinkgoHelper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())
	server := receiver.GRPCServer(requireAuth)
	run := task.StartTask("Test OTLP gRPC", func(_ cctx.Context, _ *task.Task) (bool, error) {
		err := server.Serve(listener)
		if err == grpc.ErrServerStopped {
			err = nil
		}
		return true, err
	})
	DeferCleanup(func() {
		server.Stop()
		_, err := run.GetResult()
		Expect(err).NotTo(HaveOccurred())
	})
	return listener.Addr().String()
}

var _ = Describe("standard SDK exporters", func() {
	DescribeTable("receives real SDK spans without arming", func(transport string) {
		receiver, store := testReceiver(DefaultOptions())
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var exporter sdktrace.SpanExporter
		var err error
		if transport == "http" {
			server := httptest.NewServer(receiver.HTTPHandler(false))
			DeferCleanup(server.Close)
			exporter, err = otlptracehttp.New(ctx, otlptracehttp.WithEndpoint(strings.TrimPrefix(server.URL, "http://")), otlptracehttp.WithInsecure(), otlptracehttp.WithCompression(otlptracehttp.GzipCompression))
		} else {
			exporter, err = otlptracegrpc.New(ctx, otlptracegrpc.WithEndpoint(serveGRPC(receiver, false)), otlptracegrpc.WithInsecure(), otlptracegrpc.WithCompressor("gzip"))
		}
		Expect(err).NotTo(HaveOccurred())
		provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter), sdktrace.WithResource(resource.NewSchemaless(attribute.String("service.name", "checkout"), attribute.Int("replicas", 3))))
		DeferCleanup(func() {
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			Expect(provider.Shutdown(shutdown)).To(Succeed())
		})
		tracer := provider.Tracer("example-sdk", trace.WithInstrumentationVersion("1.0"))
		parent, root := tracer.Start(ctx, "order")
		_, child := tracer.Start(parent, "charge", trace.WithAttributes(attribute.Bool("cached", false)))
		child.AddEvent("declined", trace.WithAttributes(attribute.String("password", "private-event-value")))
		child.End()
		root.End()
		Expect(provider.ForceFlush(ctx)).To(Succeed())
		spans, err := store.Read(ctx, ReadOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(spans).To(HaveLen(2))
		Expect(spans).To(ContainElement(And(HaveField("Operation", "charge"), HaveField("ParentID", root.SpanContext().SpanID().String()))))
		for _, span := range spans {
			native, err := NativeJSON(span)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(native)).To(ContainSubstring("example-sdk"))
			Expect(string(native)).NotTo(ContainSubstring("private-event-value"))
		}
	}, Entry("HTTP protobuf and gzip", "http"), Entry("gRPC and gzip", "grpc"))
})
