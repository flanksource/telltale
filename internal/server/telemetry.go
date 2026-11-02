package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/flanksource/telltale/internal/lifetime"
	"github.com/flanksource/telltale/internal/otelreceiver"
	"github.com/flanksource/telltale/internal/selftrace"
	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/credentials/apitoken"
	"google.golang.org/grpc"
)

type telemetry struct {
	store    *otelreceiver.Store
	receiver *otelreceiver.Receiver
	listener net.Listener
	server   *grpc.Server
	run      *lifetime.Service
	tracer   *selftrace.Tracer
}

func openTelemetry(ctx context.Context, options Options) (_ *telemetry, err error) {
	t := &telemetry{}
	options.OTLP.Enabled = options.OTLP.Enabled || options.SelfTrace
	defer func() {
		if err != nil {
			err = errors.Join(err, t.close())
		}
	}()
	// Without --otlp the server writes no spans; the receiver pseudo-run reads a
	// store an earlier receiver left (telltale.Reopen).
	if !options.OTLP.Enabled {
		return t, nil
	}
	if err := options.OTLP.Validate(); err != nil {
		return nil, err
	}
	t.listener, err = net.Listen("tcp", options.OTLP.GRPCListen)
	if err != nil {
		return nil, fmt.Errorf("listen OTLP gRPC: %w", err)
	}
	if options.SelfTrace && !t.listener.Addr().(*net.TCPAddr).IP.IsLoopback() {
		return nil, fmt.Errorf("self-trace requires a loopback OTLP gRPC listener")
	}
	t.store, err = otelreceiver.OpenStore(otelreceiver.StoreOptions{DataDir: options.DataDir, Retention: options.OTLP.Retention, Writable: true})
	if err != nil {
		return nil, err
	}
	verifier := apitoken.NewVerifier(agentclient.NewTokenStore(options.ConfigDirs).Lookup)
	t.receiver, err = otelreceiver.New(otelreceiver.Config{Store: t.store, Options: options.OTLP, Verifier: verifier})
	if err != nil {
		return nil, err
	}
	t.server = t.receiver.GRPCServer(!t.listener.Addr().(*net.TCPAddr).IP.IsLoopback())
	if options.SelfTrace {
		t.tracer, err = selftrace.New(ctx, selftrace.Options{Endpoint: t.listener.Addr().String(), Version: options.Version})
		if err != nil {
			return nil, err
		}
	}
	return t, nil
}

func (t *telemetry) httpHandler(requireAuth bool) http.Handler {
	if t.receiver == nil {
		return http.NotFoundHandler()
	}
	return t.receiver.HTTPHandler(requireAuth)
}

func (t *telemetry) start(cancel context.CancelCauseFunc) {
	if t.server == nil {
		return
	}
	t.run = lifetime.Start("OTLP gRPC server", func() error {
		err := t.server.Serve(t.listener)
		if err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			cancel(fmt.Errorf("OTLP gRPC server: %w", err))
			return err
		}
		return nil
	})
}

func (t *telemetry) close() error {
	var err error
	if t.tracer != nil {
		flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = t.tracer.Close(flushCtx)
		cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if t.receiver != nil {
		err = errors.Join(err, t.receiver.Stop(ctx))
	}
	if t.server != nil {
		stop := lifetime.Start("Drain OTLP gRPC", func() error {
			t.server.GracefulStop()
			return nil
		})
		select {
		case <-stop.Done():
		case <-ctx.Done():
			t.server.Stop()
			err = errors.Join(err, ctx.Err())
		}
		err = errors.Join(err, stop.Wait())
	}
	if t.run != nil {
		err = errors.Join(err, t.run.Wait())
	}
	if t.listener != nil {
		if closeErr := t.listener.Close(); !errors.Is(closeErr, net.ErrClosed) {
			err = errors.Join(err, closeErr)
		}
	}
	if t.store != nil {
		err = errors.Join(err, t.store.Close())
	}
	return err
}
