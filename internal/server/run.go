package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/flanksource/clicky/task"
	cctx "github.com/flanksource/commons/context"
	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/internal/otelreceiver"
	"github.com/flanksource/telltale/internal/rules"
	"github.com/flanksource/telltale/pkg/datadir"
	"github.com/flanksource/telltale/pkg/definition"
	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/webapp"
	"github.com/spf13/cobra"
)

type Options struct {
	Root         *cobra.Command
	Version      string
	Host         string
	Port         int
	Dev          bool
	UIPort       int
	WebDirectory string
	// DataDir is the cache root holding the run stores, the OTLP receiver's
	// store and the rule event journal.
	DataDir string
	// ConfigDirs hold the agent tokens and groups catalog, first one winning.
	ConfigDirs   []datadir.ConfigDir
	RulesFile    string
	EnforceRules bool
	OTLP         otelreceiver.Options
	SelfTrace    bool
}

func Run(ctx context.Context, options Options) (err error) {
	if options.Root == nil {
		return fmt.Errorf("command root is required")
	}
	if options.DataDir == "" || len(options.ConfigDirs) == 0 {
		return fmt.Errorf("serve: a data dir and config dirs are required (got %q, %v)", options.DataDir, options.ConfigDirs)
	}
	if options.Port < 0 || options.Port > 65535 || options.UIPort < 0 || options.UIPort > 65535 {
		return fmt.Errorf("port must be 0..65535 and ui-port must be 0..65535")
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	if options.EnforceRules && options.RulesFile == "" {
		return fmt.Errorf("--enforce-rules requires --rules")
	}
	var monitor *rules.Service
	if options.RulesFile != "" {
		monitor, err = rules.Open(options.DataDir, options.RulesFile, options.EnforceRules)
		if err != nil {
			return err
		}
		monitor.Start(ctx)
		defer func() { err = errors.Join(err, monitor.Close()) }()
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(options.Host, strconv.Itoa(options.Port)))
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer func() {
		if closeErr := listener.Close(); closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
			err = errors.Join(err, closeErr)
		}
	}()
	apiURL := "http://" + listener.Addr().String()
	telemetry, err := openTelemetry(ctx, options)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, telemetry.close()) }()
	connections, err := telltale.OpenConnections()
	if err != nil {
		return err
	}
	definitions, err := definition.NewService(definition.NewStore(options.ConfigDirs), connections)
	if err != nil {
		return err
	}
	traceMount, err := telltale.NewHandler(telltale.HandlerOptions{DataDir: options.DataDir, Prefix: TracesMount, Connections: connections, Definitions: definitions})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, traceMount.Close()) }()
	top := NewTopHandler()
	requireAPIAuth := !listener.Addr().(*net.TCPAddr).IP.IsLoopback()
	defer func() { err = errors.Join(err, top.Close()) }()
	handlerOptions := HandlerOptions{Root: options.Root, Version: options.Version, DataDir: options.DataDir, ConfigDirs: options.ConfigDirs, RequireAPIAuth: requireAPIAuth, Traces: traceMount, Top: top, Rules: monitor, Connections: connections, OTLP: telemetry.httpHandler(requireAPIAuth), SelfTrace: telemetry.tracer}
	var handler http.Handler
	if options.Dev {
		var vite *viteServer
		vite, err = startVite(ctx, devOptions{Options: options, apiURL: apiURL, cancel: cancel})
		if err == nil {
			defer func() { err = errors.Join(err, vite.stop()) }()
			handler, err = routes(handlerOptions, vite.proxy())
		}
	} else {
		handler, err = servingHandler(handlerOptions)
	}
	if err != nil {
		return err
	}
	telemetry.start(cancel)
	dataDir, err := filepath.Abs(options.DataDir)
	if err != nil {
		return fmt.Errorf("resolve data dir %q: %w", options.DataDir, err)
	}
	dirs := storageDirs{Runs: filepath.Join(dataDir, traces.Dir), Config: options.ConfigDirs, Connections: connections.Layers()}
	if telemetry.store != nil {
		dirs.Spans = otelreceiver.StorePath(dataDir)
	}
	if monitor != nil {
		dirs.RuleEvents = rules.JournalPath(dataDir)
	}
	if _, err := fmt.Fprint(options.Root.OutOrStdout(), dirs); err != nil {
		return fmt.Errorf("write storage directories: %w", err)
	}
	if telemetry.listener != nil {
		endpoints := otlpEndpoints{
			GRPC:     telemetry.listener.Addr(),
			API:      listener.Addr(),
			GRPCAuth: !telemetry.listener.Addr().(*net.TCPAddr).IP.IsLoopback(),
			HTTPAuth: requireAPIAuth,
		}
		if endpoints.GRPCAuth || requireAPIAuth {
			if endpoints.Host, err = os.Hostname(); err != nil {
				return fmt.Errorf("name this host for the OTLP endpoints: %w", err)
			}
		}
		if _, err := fmt.Fprint(options.Root.OutOrStdout(), endpoints); err != nil {
			return fmt.Errorf("write OTLP endpoints: %w", err)
		}
	}
	if _, err := fmt.Fprintf(options.Root.OutOrStdout(), "Telltale API: %s\n", apiURL); err != nil {
		return fmt.Errorf("write server URL: %w", err)
	}
	err = serve(ctx, httpOptions{listener: listener, handler: handler})
	if cause := context.Cause(ctx); cause != nil && !errors.Is(cause, context.Canceled) {
		err = errors.Join(err, cause)
	}
	return err
}

func servingHandler(options HandlerOptions) (http.Handler, error) {
	assets, err := webapp.Assets()
	if err != nil {
		return nil, err
	}
	options.Assets = assets
	return NewHandler(options)
}

type httpOptions struct {
	listener net.Listener
	handler  http.Handler
}

func serve(ctx context.Context, options httpOptions) error {
	task.SetNoRender(true)
	httpServer := &http.Server{Handler: options.handler, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: time.Minute}
	done := make(chan struct{})
	run := task.StartTask("HTTP server", func(_ cctx.Context, _ *task.Task) (bool, error) {
		defer close(done)
		err := httpServer.Serve(options.listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		return true, err
	})
	select {
	case <-ctx.Done():
	case <-done:
	}
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	shutdownErr := httpServer.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		shutdownErr = errors.Join(shutdownErr, httpServer.Close())
	}
	_, serveErr := run.GetResult()
	return errors.Join(shutdownErr, serveErr)
}
