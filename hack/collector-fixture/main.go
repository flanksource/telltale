package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/config/configgrpc"
	"go.opentelemetry.io/collector/config/configoptional"
	"go.opentelemetry.io/collector/config/configtls"
	"go.opentelemetry.io/collector/exporter"
	"go.opentelemetry.io/collector/exporter/otlpexporter"
	"go.opentelemetry.io/collector/exporter/xexporter"
	"go.opentelemetry.io/collector/receiver"
	"go.opentelemetry.io/collector/receiver/otlpreceiver"
	"go.opentelemetry.io/collector/receiver/xreceiver"
)

type collectorOptions struct {
	Listen, Endpoint, QueueSizer string
}

func main() {
	listen := flag.String("listen", "", "Collector HTTP listener")
	endpoint := flag.String("export", "", "Telltale gRPC endpoint")
	queueSizer := flag.String("queue-sizer", "requests", "Sending queue unit: requests or items")
	flag.Parse()
	if err := run(collectorOptions{Listen: *listen, Endpoint: *endpoint, QueueSizer: *queueSizer}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(options collectorOptions) (result error) {
	if options.Listen == "" || options.Endpoint == "" {
		return fmt.Errorf("listen and export endpoints are required")
	}
	if options.QueueSizer != "requests" && options.QueueSizer != "items" {
		return fmt.Errorf("queue-sizer must be requests or items, received %q", options.QueueSizer)
	}
	ctx := context.Background()
	host := componenttest.NewNopHost()
	telemetry := componenttest.NewNopTelemetrySettings()
	exportFactory := otlpexporter.NewFactory()
	exportConfig := exportFactory.CreateDefaultConfig().(*otlpexporter.Config)
	exportConfig.ClientConfig.Endpoint = options.Endpoint
	exportConfig.ClientConfig.TLS = configtls.ClientConfig{Insecure: true}
	queue := exportConfig.QueueConfig.GetOrInsertDefault()
	queue.NumConsumers = 1
	if err := queue.Sizer.UnmarshalText([]byte(options.QueueSizer)); err != nil {
		return fmt.Errorf("configure Collector queue sizing: %w", err)
	}
	exported, err := exportFactory.(xexporter.Factory).CreateProfiles(ctx, exporter.Settings{
		ID: component.MustNewID("otlp"), TelemetrySettings: telemetry,
	}, exportConfig)
	if err != nil {
		return fmt.Errorf("create Collector exporter: %w", err)
	}
	if err = exported.Start(ctx, host); err != nil {
		return fmt.Errorf("start Collector exporter: %w", err)
	}
	defer func() {
		shutdown, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		result = errors.Join(result, exported.Shutdown(shutdown))
	}()
	receiveFactory := otlpreceiver.NewFactory()
	receiveConfig := receiveFactory.CreateDefaultConfig().(*otlpreceiver.Config)
	receiveConfig.Protocols.GRPC = configoptional.None[configgrpc.ServerConfig]()
	httpConfig := receiveConfig.Protocols.HTTP.GetOrInsertDefault()
	httpConfig.ServerConfig.NetAddr.Endpoint = options.Listen
	received, err := receiveFactory.(xreceiver.Factory).CreateProfiles(ctx, receiver.Settings{
		ID: component.MustNewID("otlp"), TelemetrySettings: telemetry,
	}, receiveConfig, exported)
	if err != nil {
		return fmt.Errorf("create Collector receiver: %w", err)
	}
	if err = received.Start(ctx, host); err != nil {
		return fmt.Errorf("start Collector receiver: %w", err)
	}
	fmt.Println("ready")
	_, readErr := io.Copy(io.Discard, os.Stdin)
	shutdown, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := received.Shutdown(shutdown); err != nil {
		return err
	}
	return readErr
}
