// A local PostgreSQL lock wait recording for manual Top UI and TUI checks.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/flanksource/commons-db/connection/crd"
	"github.com/flanksource/commons-db/dbtest"
	"github.com/flanksource/commons-db/types"
	"github.com/flanksource/telltale"
)

func main() {
	if err := record(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func record() (err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, closeDB, err := dbtest.Open(dbtest.Options{Name: "sample_top_demo", DataDir: ".tmp/top-ux-review/postgres"})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, closeDB()) }()
	pool := db.SQL()
	if _, err := pool.ExecContext(ctx, "CREATE TABLE orders (id integer PRIMARY KEY, paid boolean); INSERT INTO orders VALUES (1, false)"); err != nil {
		return err
	}
	holder, err := pool.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, holder.Close()) }()
	if _, err := holder.ExecContext(ctx, "BEGIN; UPDATE orders SET paid = true WHERE id = 1"); err != nil {
		return err
	}
	defer func() {
		_, rollback := holder.ExecContext(context.Background(), "ROLLBACK")
		err = errors.Join(err, rollback)
	}()
	waiter, err := pool.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, waiter.Close()) }()
	waitCtx, stopWait := context.WithCancel(ctx)
	defer stopWait()
	waited := make(chan error, 1)
	go func() {
		_, err := waiter.ExecContext(waitCtx, "UPDATE orders SET paid = true WHERE id = 1")
		waited <- err
	}()
	defer func() {
		select {
		case waitErr := <-waited:
			err = errors.Join(err, fmt.Errorf("waiter completed before cancellation: %v", waitErr))
		default:
			stopWait()
			if waitErr := <-waited; waitErr == nil {
				err = errors.Join(err, errors.New("waiter did not report cancellation"))
			}
		}
	}()
	connection := crd.Connection{}
	connection.Name = "top-demo"
	connection.Spec = crd.ConnectionSpec{Type: "postgres", URL: types.EnvVar{ValueStatic: db.DSN()}}
	run, err := telltale.Start(ctx, telltale.StartOptions{DataDir: ".tmp/top-ux-review/data", Duration: 3 * time.Second, RunConnections: []crd.Connection{connection}, Traces: []telltale.TraceSpec{{Kind: "sql_top", Params: json.RawMessage(`{"connection":"connection://top-demo","poll":"1s","top":50}`)}}})
	if err != nil {
		return err
	}
	result, err := run.Wait()
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"runId": run.ID(), "dir": run.Dir(), "captures": result.Captures})
}
