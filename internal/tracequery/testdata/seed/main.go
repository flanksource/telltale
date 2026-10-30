package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/tracing"
	sdk "github.com/flanksource/telltale/sdk"
)

func main() {
	if err := seed(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func seed() (err error) {
	dir := flag.String("data-dir", "", "Directory for the test trace store")
	flag.Parse()
	if *dir == "" {
		return fmt.Errorf("--data-dir is required")
	}
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Minute)
	host, err := traces.Open(traces.OpenOptions{DataDir: *dir, RunID: "run-otel-fixture"})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, host.Close(ctx)) }()
	traceID := "11111111111111111111111111111111"
	spans := []sdk.Span{
		{TraceID: traceID, SpanID: "1111111111111111", Operation: "POST /orders", Kind: "server", StartedAt: at, DurationMs: 120, Status: "ok", Resource: map[string]any{"service.name": "checkout", "deployment.environment": "test"}, Attributes: map[string]any{"http.method": "POST", "order.id": "example"}},
		{TraceID: traceID, SpanID: "2222222222222222", ParentID: "1111111111111111", Operation: "charge", Kind: "client", StartedAt: at.Add(20 * time.Millisecond), DurationMs: 60, Status: "error", Resource: map[string]any{"service.name": "payments"}, Attributes: map[string]any{"http.status_code": 500, "error.message": "payment declined"}},
		{TraceID: traceID, SpanID: "3333333333333333", ParentID: "2222222222222222", Operation: "SELECT account", Kind: "client", StartedAt: at.Add(30 * time.Millisecond), DurationMs: 20, Status: "ok", Resource: map[string]any{"service.name": "database"}, Attributes: map[string]any{"db.system": "postgresql"}},
		{TraceID: "22222222222222222222222222222222", SpanID: "4444444444444444", Operation: "GET /health", Kind: "server", StartedAt: at.Add(time.Second), DurationMs: 2, Status: "ok", Resource: map[string]any{"service.name": "checkout"}},
	}
	imported := []sdk.Span{{TraceID: "33333333333333333333333333333333", SpanID: "5555555555555555", Service: "search", Operation: "lookup", StartedAt: at, DurationMs: 8, Status: "ok", Source: sdk.SpanSource{Type: sdk.SpanSourceOpenSearch, Index: "otel-traces-example", ID: "example"}}}
	for stream, rows := range map[string][]sdk.Span{"sdk-spans": spans, "imported-spans": imported} {
		for i := range rows {
			if err := rows[i].Normalize(); err != nil {
				return err
			}
		}
		if _, err = recordstore.AppendTyped(ctx, host.Runtime().Results.Backend, stream, "spans", rows); err != nil {
			return err
		}
	}
	manifest := telltale.Result{RunID: host.RunID(), Dir: host.Dir(), StartedAt: at, EndedAt: time.Now(), Captures: []telltale.CaptureResult{{Kind: "spans", Ref: tracing.TraceRef{Kind: "spans", Stream: "sdk-spans"}}, {Kind: "spans", Ref: tracing.TraceRef{Kind: "spans", Stream: "imported-spans"}}}}
	data, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(host.Dir(), telltale.ManifestFile), data, 0600); err != nil {
		return err
	}
	fmt.Println(host.RunID())
	return nil
}
