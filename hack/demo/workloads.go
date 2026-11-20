package main

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/flanksource/clicky"
	clickyexec "github.com/flanksource/clicky/exec"
	"github.com/flanksource/clicky/task"
	"github.com/flanksource/commons-db/observability"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/commons-db/query/providers/sqlite"
	commonsctx "github.com/flanksource/commons/context"
	"github.com/flanksource/telltale/pkg/tracing/terminal"
	sdk "github.com/flanksource/telltale/sdk"
)

//go:embed DemoWorkload.java
var javaSource []byte

type localWorkload struct {
	url      string
	db       *sql.DB
	server   *http.Server
	serve    task.TypedTask[struct{}]
	requests atomic.Int64
}

func startLocal(ctx context.Context, dir string) (*localWorkload, error) {
	db, err := sql.Open("sqlite", filepath.Join(dir, "orders.sqlite"))
	if err != nil {
		return nil, err
	}
	if _, err := db.ExecContext(ctx, "CREATE TABLE orders (id INTEGER PRIMARY KEY, name TEXT); INSERT INTO orders VALUES (1, 'acme checkout'), (2, 'acme shipment')"); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, errors.Join(err, db.Close())
	}
	w := &localWorkload{db: db, url: "http://" + listener.Addr().String()}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /orders", w.orders)
	mux.HandleFunc("GET /metrics", func(out http.ResponseWriter, _ *http.Request) {
		out.Header().Set("Content-Type", "text/plain; version=0.0.4")
		if _, err := fmt.Fprintf(out, "# TYPE demo_requests_total counter\ndemo_requests_total %d\n", w.requests.Load()); err != nil {
			slog.Error("write metrics", "error", err)
		}
	})
	w.server = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	w.serve = clicky.StartTask("Demo HTTP server", func(_ commonsctx.Context, _ *task.Task) (struct{}, error) {
		err := w.server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		return struct{}{}, err
	}, task.WithContext(ctx), task.WithCancellationDrain())
	return w, nil
}

func (w *localWorkload) orders(out http.ResponseWriter, request *http.Request) {
	ctx, end := sdk.StartSpan(request.Context(), "demo.orders", "service.name", "telltale-demo")
	result, err := sqlite.ReadSQLPage(ctx, w.db, "sqlite", sqlite.SQLPageRequest{Connection: "demo", Query: "SELECT id, name FROM orders", Page: query.PageRequest{Limit: 10}})
	end(err)
	if err != nil {
		http.Error(out, "read demo orders", http.StatusInternalServerError)
		return
	}
	w.requests.Add(1)
	slog.New(sdk.SlogHandler(slog.NewTextHandler(io.Discard, nil))).InfoContext(ctx, "demo orders read", "rows", len(result.Rows))
	out.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(out).Encode(result.Rows); err != nil {
		slog.Error("write orders", "error", err)
	}
}

func (w *localWorkload) request(ctx context.Context) error {
	client := &http.Client{Transport: observability.ApplyHTTPObservability(ctx, "demo", nil, nil), Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, w.url+"/orders", nil)
	if err != nil {
		return err
	}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	_, readErr := io.Copy(io.Discard, response.Body)
	if response.StatusCode != http.StatusOK {
		readErr = errors.Join(readErr, fmt.Errorf("orders returned %d", response.StatusCode))
	}
	return errors.Join(readErr, response.Body.Close())
}

func (w *localWorkload) close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := w.server.Shutdown(ctx)
	if err != nil {
		err = errors.Join(err, w.server.Close())
	}
	_, serveErr := w.serve.GetResult()
	return errors.Join(err, serveErr, w.db.Close())
}

type javaWorkload struct {
	cmd    *exec.Cmd
	input  io.WriteCloser
	output *os.File
}

func startJava(ctx context.Context, dir string) (*javaWorkload, error) {
	source := filepath.Join(dir, "DemoWorkload.java")
	if err := os.WriteFile(source, javaSource, 0o600); err != nil {
		return nil, err
	}
	if result := clickyexec.NewExec("javac", "-d", dir, source).WithContext(ctx).Run().Result(); result.Error != nil {
		return nil, fmt.Errorf("compile demo JVM: %w", result.Error)
	}
	output, err := os.Create(filepath.Join(dir, "jvm.log"))
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "java", "-Xmx64m", "-cp", dir, "DemoWorkload")
	cmd.Stdout, cmd.Stderr = output, output
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, errors.Join(err, output.Close())
	}
	if err := cmd.Start(); err != nil {
		return nil, errors.Join(err, input.Close(), output.Close())
	}
	return &javaWorkload{cmd: cmd, input: input, output: output}, nil
}

func (w *javaWorkload) close() error {
	return errors.Join(w.input.Close(), w.cmd.Wait(), w.output.Close())
}

func recordTerminal(ctx context.Context, stream string) (err error) {
	writer, err := terminal.Writer(stream)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, writer.Close()) }()
	cmd := exec.CommandContext(ctx, "git", "--version")
	file, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 100})
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(writer, file)
	if errors.Is(copyErr, syscall.EIO) {
		copyErr = nil
	}
	waitErr := cmd.Wait()
	return errors.Join(copyErr, waitErr, terminal.Resize(stream, 120, 30), terminal.SetExitCode(stream, cmd.ProcessState.ExitCode()), file.Close())
}
