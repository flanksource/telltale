package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/flanksource/clicky/task"
	cctx "github.com/flanksource/commons/context"
	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/internal/rules"
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
	// WorkDir holds the run stores `telltale trace run` recorded.
	WorkDir      string
	RulesFile    string
	EnforceRules bool
}

func Run(ctx context.Context, options Options) (err error) {
	if options.Root == nil {
		return fmt.Errorf("command root is required")
	}
	if options.Port < 0 || options.Port > 65535 || options.UIPort < 1 || options.UIPort > 65535 {
		return fmt.Errorf("port must be 0..65535 and ui-port must be 1..65535")
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	if options.EnforceRules && options.RulesFile == "" {
		return fmt.Errorf("--enforce-rules requires --rules")
	}
	var monitor *rules.Service
	if options.RulesFile != "" {
		monitor, err = rules.Open(options.WorkDir, options.RulesFile, options.EnforceRules)
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
	connections, err := telltale.OpenConnections()
	if err != nil {
		return err
	}
	traceMount, err := telltale.NewHandler(telltale.HandlerOptions{WorkDir: options.WorkDir, Prefix: TracesMount, Connections: connections})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, traceMount.Close()) }()
	top := NewTopHandler(options.WorkDir)
	defer func() { err = errors.Join(err, top.Close()) }()
	var handler http.Handler
	if options.Dev {
		var vite *viteServer
		vite, err = startVite(ctx, devOptions{Options: options, apiURL: apiURL, cancel: cancel})
		if err == nil {
			defer func() { err = errors.Join(err, vite.stop()) }()
			handler = routes(HandlerOptions{Root: options.Root, Version: options.Version, Traces: traceMount, Top: top, Rules: monitor}, vite.proxy())
		}
	} else {
		handler, err = servingHandler(options, traceMount, top, monitor)
	}
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(options.Root.OutOrStdout(), "Telltale API: %s\n", apiURL); err != nil {
		return fmt.Errorf("write server URL: %w", err)
	}
	err = serve(ctx, listener, handler)
	if cause := context.Cause(ctx); cause != nil && !errors.Is(cause, context.Canceled) {
		err = errors.Join(err, cause)
	}
	return err
}

func servingHandler(options Options, traces, top http.Handler, monitor *rules.Service) (http.Handler, error) {
	assets, err := webapp.Assets()
	if err != nil {
		return nil, err
	}
	return NewHandler(HandlerOptions{Root: options.Root, Version: options.Version, Assets: assets, Traces: traces, Top: top, Rules: monitor})
}

func serve(ctx context.Context, listener net.Listener, handler http.Handler) error {
	task.SetNoRender(true)
	httpServer := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: time.Minute}
	done := make(chan struct{})
	run := task.StartTask("HTTP server", func(_ cctx.Context, _ *task.Task) (bool, error) {
		defer close(done)
		err := httpServer.Serve(listener)
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
