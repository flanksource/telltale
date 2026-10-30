package selftrace

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/flanksource/clicky/task"
	cctx "github.com/flanksource/commons/context"
	"github.com/flanksource/telltale/internal/otelreceiver"
	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/credentials/apitoken"
	"github.com/flanksource/telltale/pkg/datadir"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
)

var _ = Describe("local exporter integration", func() {
	It("flushes through the actual loopback gRPC receiver despite an ambient remote endpoint", func() {
		GinkgoT().Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://example.invalid:1")
		GinkgoT().Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "https://example.invalid:2")
		dir := GinkgoT().TempDir()
		options := otelreceiver.DefaultOptions()
		store, err := otelreceiver.OpenStore(otelreceiver.StoreOptions{DataDir: dir, Retention: options.Retention, Writable: true})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(store.Close)
		receiver, err := otelreceiver.New(otelreceiver.Config{Store: store, Options: options, Verifier: apitoken.NewVerifier(agentclient.NewTokenStore([]datadir.ConfigDir{{Name: datadir.ConfigGlobal, Dir: dir}}).Lookup)})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(receiver.Stop(context.Background())).To(Succeed()) })
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		server := receiver.GRPCServer(false)
		run := task.StartTask("Self-trace test receiver", func(_ cctx.Context, _ *task.Task) (bool, error) {
			err := server.Serve(listener)
			if err == grpc.ErrServerStopped {
				err = nil
			}
			return true, err
		})
		DeferCleanup(func() { server.Stop(); _, err := run.GetResult(); Expect(err).NotTo(HaveOccurred()) })
		tracer, err := New(context.Background(), Options{Endpoint: listener.Addr().String(), Version: "test-version"})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(tracer.Close(context.Background())).To(Succeed()) })
		tracer.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) })).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/health", nil))
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		Expect(tracer.Close(ctx)).To(Succeed())
		spans, err := store.Read(ctx, otelreceiver.ReadOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(spans).To(HaveLen(1))
		Expect(spans[0].Service).To(Equal("telltale"))
		Expect(spans[0].Resource).To(HaveKeyWithValue("service.version", "test-version"))
		Expect(spans[0].Attributes).To(HaveKeyWithValue("http.response.status_code", int64(http.StatusAccepted)))
	})
	DescribeTable("rejects invalid destinations before constructing a tracer", func(endpoint string) {
		_, err := New(context.Background(), Options{Endpoint: endpoint, Version: "test"})
		Expect(err).To(HaveOccurred())
	}, Entry("remote", "example.invalid:4317"), Entry("wildcard", "0.0.0.0:4317"), Entry("unassigned port", "127.0.0.1:0"))
	It("surfaces a bounded flush failure when the receiver is unavailable", func() {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		endpoint := listener.Addr().String()
		Expect(listener.Close()).To(Succeed())
		tracer, err := New(context.Background(), Options{Endpoint: endpoint, Version: "test"})
		Expect(err).NotTo(HaveOccurred())
		tracer.Handler(http.NotFoundHandler()).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/unavailable", nil))
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		Expect(tracer.Close(ctx)).To(HaveOccurred())
	})
})
