package cli

import (
	"fmt"

	"github.com/flanksource/clicky"
	"github.com/flanksource/telltale/internal/otelreceiver"
	"github.com/flanksource/telltale/internal/server"
	"github.com/flanksource/telltale/pkg/datadir"
	"github.com/flanksource/telltale/pkg/definition"
	"github.com/spf13/cobra"
)

func NewRoot(version string) *cobra.Command {
	root := &cobra.Command{
		Use: "telltale", Short: "Telltale", Version: version, SilenceUsage: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			clicky.Flags.UseFlags()
			if err := resolveDataDir(cmd); err != nil {
				return err
			}
			return EnableHTTPWireLogging()
		},
	}
	clicky.BindAllFlags(root.PersistentFlags(), "format")
	options := server.Options{Root: root, Version: version, Host: "localhost", Port: 8080, WebDirectory: "webapp", OTLP: otelreceiver.DefaultOptions()}
	serve := &cobra.Command{
		Use: "serve", Short: "Start the HTTP API and web UI", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var err error
			if options.ConfigDirs, err = datadir.ConfigDirs(); err != nil {
				return err
			}
			return server.Run(cmd.Context(), options)
		},
	}
	serve.Flags().StringVar(&options.Host, "host", options.Host, "API bind address")
	serve.Flags().IntVar(&options.Port, "port", options.Port, "API port (0 selects a free port)")
	serve.Flags().BoolVar(&options.Dev, "dev", false, "Start Vite with local Clicky UI source and HMR")
	serve.Flags().IntVar(&options.UIPort, "ui-port", options.UIPort, "Vite port in development (0 selects a free port)")
	serve.Flags().StringVar(&options.WebDirectory, "web-dir", options.WebDirectory, "Frontend source directory for --dev")
	bindDataDir(serve.Flags(), &options.DataDir)
	serve.Flags().StringVar(&options.RulesFile, "rules", "", "Continuously observe resource rules from this YAML file")
	serve.Flags().BoolVar(&options.EnforceRules, "enforce-rules", false, "Execute resource rule hooks (default: observation only)")
	serve.Flags().BoolVar(&options.OTLP.Enabled, "otlp", false, "Continuously receive OTLP traces into local storage")
	serve.Flags().BoolVar(&options.SelfTrace, "self-trace", false, "Trace HTTP requests to the local OTLP receiver (enables --otlp)")
	serve.Flags().StringVar(&options.OTLP.GRPCListen, "otlp-grpc-listen", options.OTLP.GRPCListen, "OTLP gRPC listen address")
	serve.Flags().DurationVar(&options.OTLP.Retention, "otlp-retention", options.OTLP.Retention, "Received span retention from ingestion time")
	serve.Flags().IntVar(&options.OTLP.MaxRequestBytes, "otlp-max-request-bytes", options.OTLP.MaxRequestBytes, "Maximum OTLP request bytes before and after decompression")
	serve.Flags().IntVar(&options.OTLP.MaxSpans, "otlp-max-spans", options.OTLP.MaxSpans, "Maximum spans per OTLP request")
	serve.Flags().IntVar(&options.OTLP.MaxInflight, "otlp-max-inflight", options.OTLP.MaxInflight, "Maximum concurrent OTLP exports across both transports")
	root.AddCommand(serve, newTraceCommand(), newConnectionCommand(), newRunCommand(), newTopCommand(), newRulesCommand(), newAgentsCommand())
	root.AddCommand(newRecordsCommand(), newDefinitionsCommand())
	builtins, err := definition.Builtins()
	if err != nil {
		panic(err)
	}
	for _, def := range builtins {
		if existing, _, err := root.Find([]string{def.Name}); err == nil && existing != root {
			panic(fmt.Sprintf("built-in trace definition %q is named as the %q command", def.Name, existing.CommandPath()))
		}
		root.AddCommand(newPresetCommand(def))
	}
	return root
}
