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
	imported "github.com/flanksource/telltale/pkg/tracing/opensearch"
	sdk "github.com/flanksource/telltale/sdk"
)

func main() {
	if err := seed(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func seed() (err error) {
	dir := flag.String("work-dir", "", "Directory for the test trace store")
	flag.Parse()
	if *dir == "" {
		return fmt.Errorf("--work-dir is required")
	}
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Minute)
	host, err := traces.Open(traces.OpenOptions{WorkDir: *dir, RunID: "run-otel-fixture"})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, host.Close(ctx)) }()
	traceID := "11111111111111111111111111111111"
	spans := []sdk.Span{
		{TraceID: traceID, SpanID: "1111111111111111", Name: "POST /orders", Kind: "server", StartedAt: at, DurationMs: 120, Status: "Ok", Resource: map[string]string{"service.name": "checkout", "deployment.environment": "test"}, Attrs: map[string]any{"http.method": "POST", "order.id": "example"}},
		{TraceID: traceID, SpanID: "2222222222222222", ParentID: "1111111111111111", Name: "charge", Kind: "client", StartedAt: at.Add(20 * time.Millisecond), DurationMs: 60, Status: "Error", Resource: map[string]string{"service.name": "payments"}, Attrs: map[string]any{"http.status_code": 500, "error.message": "payment declined"}},
		{TraceID: traceID, SpanID: "3333333333333333", ParentID: "2222222222222222", Name: "SELECT account", Kind: "client", StartedAt: at.Add(30 * time.Millisecond), DurationMs: 20, Status: "Ok", Resource: map[string]string{"service.name": "database"}, Attrs: map[string]any{"db.system": "postgresql"}},
		{TraceID: "22222222222222222222222222222222", SpanID: "4444444444444444", Name: "GET /health", Kind: "server", StartedAt: at.Add(time.Second), DurationMs: 2, Status: "Ok", Resource: map[string]string{"service.name": "checkout"}},
	}
	if _, err = recordstore.AppendTyped(ctx, host.Runtime().Results.Backend, "sdk-spans", "spans", spans); err != nil {
		return err
	}
	if _, err = recordstore.AppendTyped(ctx, host.Runtime().Results.Backend, "imported-spans", "opensearch", []imported.Span{{TraceID: "33333333333333333333333333333333", SpanID: "5555555555555555", Service: "search", Operation: "lookup", Timestamp: at, DurationMs: 8, Status: "ok", SourceIndex: "otel-traces-example", SourceID: "example", SourceKey: "otel-traces-example/example"}}); err != nil {
		return err
	}
	manifest := telltale.Result{RunID: host.RunID(), Dir: host.Dir(), StartedAt: at, EndedAt: time.Now(), Captures: []telltale.CaptureResult{{Kind: "spans", Ref: tracing.TraceRef{Kind: "spans", Stream: "sdk-spans"}}, {Kind: "opensearch", Ref: tracing.TraceRef{Kind: "opensearch", Stream: "imported-spans"}}}}
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
