package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gorm.io/gorm/logger"
)

type options struct {
	dataDir, model, saveConnection, verifyRun, exportPlayground string
	timeout                                                     time.Duration
}

func main() {
	logger.Default = logger.New(log.New(os.Stderr, "", log.LstdFlags), logger.Config{LogLevel: logger.Warn, SlowThreshold: 200 * time.Millisecond})
	var o options
	flag.StringVar(&o.dataDir, "data-dir", ".tmp/demo/data", "Demo trace data directory")
	flag.StringVar(&o.model, "model", "", "Captain API model for one request costing at most $0.05")
	flag.StringVar(&o.saveConnection, "save-connection-from", "", "Save an existing Connection resource as shared connection://mssql, then exit")
	flag.StringVar(&o.verifyRun, "verify-run", "", "Verify an existing demo run without making another LLM call")
	flag.StringVar(&o.exportPlayground, "export-playground", "", "Export recorded ANSI events and native rows for the Clicky UI playground (requires --verify-run)")
	flag.DurationVar(&o.timeout, "timeout", 2*time.Minute, "Overall recording deadline")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := command(ctx, o); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func command(ctx context.Context, o options) error {
	if o.exportPlayground != "" {
		if o.verifyRun == "" {
			return fmt.Errorf("--export-playground requires --verify-run")
		}
		return exportPlayground(ctx, o)
	}
	if o.saveConnection != "" {
		return saveConnection(o.saveConnection)
	}
	if o.verifyRun != "" {
		return verifyStored(ctx, verification{DataDir: o.dataDir, RunID: o.verifyRun})
	}
	if o.model == "" {
		return fmt.Errorf("--model is required; use an authenticated Captain API model")
	}
	if o.timeout <= 0 {
		return fmt.Errorf("--timeout must be positive")
	}
	ctx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	return generate(ctx, o)
}
