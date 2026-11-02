package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context) (err error) {
	var exporter sdktrace.SpanExporter
	switch protocol := os.Getenv("OTEL_EXPORTER_OTLP_TRACES_PROTOCOL"); protocol {
	case "http/protobuf":
		exporter, err = otlptracehttp.New(ctx)
	case "grpc":
		exporter, err = otlptracegrpc.New(ctx)
	default:
		return fmt.Errorf("set OTEL_EXPORTER_OTLP_TRACES_PROTOCOL to http/protobuf or grpc, got %q", protocol)
	}
	if err != nil {
		return err
	}
	res, err := resource.New(ctx, resource.WithFromEnv())
	if err != nil {
		return errors.Join(err, exporter.Shutdown(ctx))
	}
	provider := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter), sdktrace.WithResource(res))
	defer func() { err = errors.Join(err, provider.Shutdown(context.WithoutCancel(ctx))) }()
	tracer := provider.Tracer("telltale-example", trace.WithInstrumentationVersion("1.0"))
	parent, root := tracer.Start(ctx, "POST /orders", trace.WithSpanKind(trace.SpanKindServer))
	_, child := tracer.Start(parent, "charge", trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(attribute.String("order.id", "example"), attribute.Bool("retryable", false)))
	child.AddEvent("payment declined", trace.WithAttributes(attribute.Int("attempt", 1)))
	child.SetStatus(codes.Error, "demonstration error")
	child.End()
	root.End()
	if err := provider.ForceFlush(ctx); err != nil {
		return err
	}
	fmt.Println("Exported trace:", root.SpanContext().TraceID())
	return nil
}
