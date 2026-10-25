package cli

import (
	"github.com/flanksource/clicky"
	"github.com/flanksource/telltale/internal/server"
	"github.com/spf13/cobra"
)

func NewRoot(version string) *cobra.Command {
	root := &cobra.Command{
		Use: "telltale", Short: "Telltale", Version: version, SilenceUsage: true,
		PersistentPreRunE: func(*cobra.Command, []string) error { return EnableHTTPWireLogging() },
	}
	clicky.BindAllFlags(root.PersistentFlags(), "format")
	options := server.Options{Root: root, Version: version, Host: "localhost", Port: 8080, UIPort: 5173, WebDirectory: "webapp", WorkDir: "."}
	serve := &cobra.Command{
		Use: "serve", Short: "Start the HTTP API and web UI", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return server.Run(cmd.Context(), options)
		},
	}
	serve.Flags().StringVar(&options.Host, "host", options.Host, "API bind address")
	serve.Flags().IntVar(&options.Port, "port", options.Port, "API port (0 selects a free port)")
	serve.Flags().BoolVar(&options.Dev, "dev", false, "Start Vite with local Clicky UI source and HMR")
	serve.Flags().IntVar(&options.UIPort, "ui-port", options.UIPort, "Vite port in development")
	serve.Flags().StringVar(&options.WebDirectory, "web-dir", options.WebDirectory, "Frontend source directory for --dev")
	serve.Flags().StringVar(&options.WorkDir, "work-dir", options.WorkDir, "Directory whose .telltale/traces run stores are served")
	root.AddCommand(serve, newTraceCommand())
	return root
}
