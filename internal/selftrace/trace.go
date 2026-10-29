package selftrace

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Options struct {
	Endpoint string
	Version  string
}

type Tracer struct {
	provider   *sdktrace.TracerProvider
	connection *grpc.ClientConn
	once       sync.Once
	mu         sync.Mutex
	exportErr  error
	closeErr   error
}

func New(ctx context.Context, options Options) (*Tracer, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateEndpoint(options.Endpoint); err != nil {
		return nil, err
	}
	connection, err := grpc.NewClient(options.Endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithNoProxy())
	if err != nil {
		return nil, fmt.Errorf("self-trace connection: %w", err)
	}
	exporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithGRPCConn(connection), otlptracegrpc.WithTimeout(5*time.Second))
	if err != nil {
		return nil, errors.Join(err, connection.Close())
	}
	tracer := &Tracer{connection: connection}
	tracer.provider = sdktrace.NewTracerProvider(
		sdktrace.WithResource(resource.NewSchemaless(attribute.String("service.name", "telltale"), attribute.String("service.version", options.Version))),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())),
		sdktrace.WithBatcher(reportingExporter{SpanExporter: exporter, tracer: tracer}, sdktrace.WithBatchTimeout(time.Second), sdktrace.WithExportTimeout(5*time.Second), sdktrace.WithMaxQueueSize(2048), sdktrace.WithMaxExportBatchSize(512), sdktrace.WithBlocking()),
	)
	return tracer, nil
}

func validateEndpoint(endpoint string) error {
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		return fmt.Errorf("self-trace destination: %w", err)
	}
	if host != "localhost" && !net.ParseIP(host).IsLoopback() {
		return fmt.Errorf("self-trace destination must be loopback, got %q", endpoint)
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return fmt.Errorf("self-trace destination requires a bound port, got %q", endpoint)
	}
	return nil
}

func (t *Tracer) Close(ctx context.Context) error {
	t.once.Do(func() {
		flushErr := t.provider.ForceFlush(ctx)
		shutdownErr := t.provider.Shutdown(ctx)
		connectionErr := t.connection.Close()
		t.mu.Lock()
		defer t.mu.Unlock()
		t.closeErr = errors.Join(flushErr, shutdownErr, connectionErr, t.exportErr)
	})
	return t.closeErr
}
