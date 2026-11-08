package cli

import (
	"context"
	"errors"
	"fmt"
	"github.com/flanksource/clicky/rpc"
	"os"
	"os/signal"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/internal/server"
	"github.com/flanksource/telltale/internal/traceui"
	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/traces/read"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func bindFollow(cmd *cobra.Command) {
	cmd.Flags().Bool("follow", false, "Print every armed capture's records while recording")
	cmd.Flags().Bool("tui", false, "Follow records in an interactive terminal")
	cmd.Flags().Bool("ui", false, "Open this run's traces in a local web viewer; keep serving until Ctrl+C")
	cmd.Flags().String("sort", "seq", "Record order: seq or timestamp")
}

func followOptions(cmd *cobra.Command) (bool, bool, bool, string, error) {
	follow, err := cmd.Flags().GetBool("follow")
	if err != nil {
		return false, false, false, "", err
	}
	tui, err := cmd.Flags().GetBool("tui")
	if err != nil {
		return false, false, false, "", err
	}
	ui, err := cmd.Flags().GetBool("ui")
	if err != nil {
		return false, false, false, "", err
	}
	sort, err := cmd.Flags().GetString("sort")
	if err != nil {
		return false, false, false, "", err
	}
	if sort != "seq" && sort != "timestamp" {
		return false, false, false, "", fmt.Errorf("unknown record sort %q", sort)
	}
	if tui && (!term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd()))) {
		return false, false, false, "", errors.New("--tui requires a terminal")
	}
	return follow || tui, tui, ui, sort, nil
}

func openRunViewer(cmd *cobra.Command, options server.ViewerOptions) (*server.Viewer, error) {
	viewer, err := server.StartViewer(options)
	if err != nil {
		return nil, err
	}
	order, err := cmd.Flags().GetString("sort")
	if err != nil {
		return nil, err
	}
	viewer.URL += "&feedSort=" + order
	if _, err := fmt.Fprintln(cmd.ErrOrStderr(), viewer.URL); err != nil {
		return nil, err
	}
	// Opening the browser is an output action; readiness is established by the listener.
	if err := rpc.OpenBrowser(viewer.URL); err != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return nil, errors.Join(err, viewer.Close(ctx))
	}
	return viewer, nil
}

func waitViewer(viewer *server.Viewer) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return viewer.Close(closeCtx)
}

func showTraceTUI(ctx context.Context, run *telltale.Run, sort string) (_ *telltale.Result, err error) {
	runtime, closeStore, err := traces.Reopen(run.Dir())
	if err != nil {
		return nil, errors.Join(err, run.Stop())
	}
	defer func() { err = errors.Join(err, closeStore()) }()
	info := run.Info()
	refs, err := read.RunRefs(info, runtime)
	if err != nil {
		return nil, err
	}
	feedCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	program := tea.NewProgram(traceui.New(feedCtx, traceui.Options{Refs: refs, Runs: []telltale.RunInfo{info}, Sort: sort}), tea.WithContext(ctx))
	go func() {
		select {
		case <-run.Done():
			info := run.Info()
			program.Send(traceui.End{Info: &info})
		case <-feedCtx.Done():
		}
	}()
	final, runErr := program.Run()
	err = runErr
	if model, ok := final.(traceui.Model); ok {
		err = errors.Join(err, model.Err())
	}
	stopErr := run.Stop()
	result, waitErr := run.Wait()
	cancel()
	return result, errors.Join(err, stopErr, waitErr)
}
