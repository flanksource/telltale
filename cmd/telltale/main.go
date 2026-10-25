package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/flanksource/telltale/internal/cli"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err := cli.NewRoot(version).ExecuteContext(ctx)
	cli.FlushHAR()
	if err != nil {
		os.Exit(1)
	}
}
