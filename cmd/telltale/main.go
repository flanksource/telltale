package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/flanksource/telltale/internal/cli"
	"github.com/flanksource/telltale/internal/shutdown"
)

var version = "dev"

func main() {
	os.Exit(execute())
}

func execute() int {
	restore, err := terminalRestore()
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return 1
	}
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	lifecycle, err := shutdown.New(context.Background(), shutdown.Options{
		GracePeriod: shutdown.GracePeriod, Signals: signals, Output: os.Stderr, Restore: restore, ForceExit: os.Exit,
	})
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer lifecycle.Close()
	err = cli.NewRoot(version).ExecuteContext(lifecycle.Context())
	cli.FlushHAR()
	if err != nil {
		return 1
	}
	return 0
}
