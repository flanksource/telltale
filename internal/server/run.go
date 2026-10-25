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
	WorkDir string
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
	traceMount := NewTraceMount(options.WorkDir)
	defer func() { err = errors.Join(err, traceMount.Close()) }()
	var handler http.Handler
	if options.Dev {
		var vite *viteServer
		vite, err = startVite(ctx, devOptions{Options: options, apiURL: apiURL, cancel: cancel})
		if err == nil {
			defer func() { err = errors.Join(err, vite.stop()) }()
			handler = routes(HandlerOptions{Root: options.Root, Version: options.Version, Traces: traceMount}, vite.proxy())
		}
	} else {
		handler, err = servingHandler(options, traceMount)
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

func servingHandler(options Options, traces http.Handler) (http.Handler, error) {
	assets, err := webapp.Assets()
	if err != nil {
		return nil, err
	}
	return NewHandler(HandlerOptions{Root: options.Root, Version: options.Version, Assets: assets, Traces: traces})
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
