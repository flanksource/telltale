package server

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/flanksource/clicky/task"
	cctx "github.com/flanksource/commons/context"
	"github.com/flanksource/telltale/internal/otelreceiver"
	"github.com/flanksource/telltale/pkg/datadir"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("self-trace lifecycle", func() {
	It("drains an active HTTP handler before flushing its span and stopping the receiver", func() {
		options := Options{DataDir: GinkgoT().TempDir(), ConfigDirs: []datadir.ConfigDir{{Name: datadir.ConfigGlobal, Dir: GinkgoT().TempDir()}}, Version: "test", SelfTrace: true, OTLP: otelreceiver.DefaultOptions()}
		options.OTLP.GRPCListen = "127.0.0.1:0"
		telemetry, err := openTelemetry(context.Background(), options)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(telemetry.close)
		ctx, cancel := context.WithCancelCause(context.Background())
		DeferCleanup(func() { cancel(nil) })
		telemetry.start(cancel)
		entered, release := make(chan struct{}), make(chan struct{})
		releaseHandler := sync.OnceFunc(func() { close(release) })
		DeferCleanup(releaseHandler)
		mux := http.NewServeMux()
		mux.HandleFunc("GET /last", func(w http.ResponseWriter, _ *http.Request) {
			close(entered)
			<-release
			w.WriteHeader(http.StatusAccepted)
		})
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		run := task.StartTask("Self-trace HTTP drain test", func(_ cctx.Context, _ *task.Task) (bool, error) {
			return true, serve(ctx, httpOptions{listener: listener, handler: telemetry.tracer.Handler(mux)})
		})
		requestCtx, cancelRequest := context.WithTimeout(context.Background(), 5*time.Second)
		DeferCleanup(cancelRequest)
		request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, "http://"+listener.Addr().String()+"/last", nil)
		Expect(err).NotTo(HaveOccurred())
		request.Header.Set("traceparent", "00-1234567890abcdef1234567890abcdef-1234567890abcdef-01")
		client := task.StartTask("Final self-traced HTTP request", func(_ cctx.Context, _ *task.Task) (int, error) {
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				return 0, err
			}
			_, readErr := io.Copy(io.Discard, response.Body)
			closeErr := response.Body.Close()
			return response.StatusCode, errors.Join(readErr, closeErr)
		})
		Eventually(entered).Should(BeClosed())
		cancel(nil)
		Eventually(func() bool {
			connection, err := net.DialTimeout("tcp", listener.Addr().String(), 50*time.Millisecond)
			if err != nil {
				return true
			}
			Expect(connection.Close()).To(Succeed())
			return false
		}).Should(BeTrue())
		releaseHandler()
		code, err := client.GetResult()
		Expect(err).NotTo(HaveOccurred())
		Expect(code).To(Equal(http.StatusAccepted))
		_, err = run.GetResult()
		Expect(err).NotTo(HaveOccurred())
		Expect(telemetry.close()).To(Succeed())
		store, err := otelreceiver.OpenStore(otelreceiver.StoreOptions{DataDir: options.DataDir, Retention: options.OTLP.Retention})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(store.Close)
		spans, err := store.Read(context.Background(), otelreceiver.ReadOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(spans).To(HaveExactElements(And(HaveField("Operation", "GET /last"), HaveField("Service", "telltale"))))
	})
	It("refuses a wildcard self-export listener before creating storage", func() {
		options := Options{DataDir: GinkgoT().TempDir(), ConfigDirs: []datadir.ConfigDir{{Name: datadir.ConfigGlobal, Dir: GinkgoT().TempDir()}}, SelfTrace: true, OTLP: otelreceiver.DefaultOptions()}
		options.OTLP.GRPCListen = "0.0.0.0:0"
		_, err := openTelemetry(context.Background(), options)
		Expect(err).To(MatchError(ContainSubstring("requires a loopback")))
		exists, err := otelreceiver.StoreExists(options.DataDir)
		Expect(err).NotTo(HaveOccurred())
		Expect(exists).To(BeFalse())
	})
})
