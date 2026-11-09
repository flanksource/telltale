package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/flanksource/clicky"
	"github.com/flanksource/clicky/task"
	"github.com/flanksource/commons-db/connection/store"
	dbcontext "github.com/flanksource/commons-db/context"
	commonsctx "github.com/flanksource/commons/context"
	mssql "github.com/microsoft/go-mssqldb"
	"github.com/microsoft/go-mssqldb/msdsn"
)

type labWorkload struct {
	admin, db   *sql.DB
	name        string
	connections *store.Store
	created     bool
}

func openLab(ctx context.Context) (*labWorkload, error) {
	connections, err := store.Open("telltale")
	if err != nil {
		return nil, err
	}
	model, err := connections.ResolveConnection(dbcontext.NewContext(ctx), "connection://mssql")
	if err != nil {
		return nil, err
	}
	if model == nil {
		return nil, fmt.Errorf("connection://mssql is not configured")
	}
	config, err := msdsn.Parse(model.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid SQL Server DSN in connection://mssql")
	}
	if config.Host != "mssql.lab" || config.Port != 31433 {
		return nil, fmt.Errorf("connection://mssql must target mssql.lab:31433")
	}
	config.Database = "master"
	admin, err := sql.Open("sqlserver", config.URL().String())
	if err != nil {
		return nil, fmt.Errorf("open lab SQL Server connection")
	}
	if err := admin.PingContext(ctx); err != nil {
		return nil, errors.Join(err, admin.Close())
	}
	var canCreate, canObserve int
	if err := admin.QueryRowContext(ctx, "SELECT HAS_PERMS_BY_NAME(NULL, NULL, 'CREATE ANY DATABASE'), HAS_PERMS_BY_NAME(NULL, NULL, 'ALTER ANY EVENT SESSION')").Scan(&canCreate, &canObserve); err != nil {
		return nil, errors.Join(err, admin.Close())
	}
	if canCreate != 1 || canObserve != 1 {
		return nil, errors.Join(fmt.Errorf("mssql needs CREATE ANY DATABASE and ALTER ANY EVENT SESSION"), admin.Close())
	}
	return &labWorkload{admin: admin, connections: connections, name: fmt.Sprintf("telltale_demo_%d", time.Now().UnixNano())}, nil
}

func (w *labWorkload) create(ctx context.Context) error {
	if _, err := w.admin.ExecContext(ctx, "CREATE DATABASE ["+w.name+"]"); err != nil {
		return err
	}
	w.created = true
	// Credentials remain in memory; the trace only names connection://mssql.
	connection, err := w.connections.ResolveConnection(dbcontext.NewContext(ctx), "connection://mssql")
	if err != nil {
		return err
	}
	config, err := msdsn.Parse(connection.URL)
	if err != nil {
		return fmt.Errorf("invalid SQL Server DSN")
	}
	config.Database, config.AppName = w.name, "telltale-demo"
	w.db, err = sql.Open("sqlserver", config.URL().String())
	if err != nil {
		return fmt.Errorf("open demo database")
	}
	_, err = w.db.ExecContext(ctx, "CREATE TABLE dbo.orders (id int PRIMARY KEY, value int); INSERT INTO dbo.orders VALUES (1, 0), (2, 0)")
	return err
}

func (w *labWorkload) query(ctx context.Context) error {
	var total int
	// The delay keeps the request running across sql_top polls.
	return w.db.QueryRowContext(ctx, "WAITFOR DELAY '00:00:01'; SELECT SUM(value) FROM dbo.orders /* telltale_demo */").Scan(&total)
}

func (w *labWorkload) deadlock(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	first, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = first.Rollback() }()
	second, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = second.Rollback() }()
	if _, err := first.ExecContext(ctx, "UPDATE dbo.orders SET value=value+1 WHERE id=1"); err != nil {
		return err
	}
	if _, err := second.ExecContext(ctx, "UPDATE dbo.orders SET value=value+1 WHERE id=2"); err != nil {
		return err
	}
	var handles []task.TypedTask[error]
	for index, tx := range []*sql.Tx{first, second} {
		id, transaction := 2-index, tx
		handles = append(handles, clicky.StartTask("Induce demo deadlock", func(_ commonsctx.Context, _ *task.Task) (error, error) {
			_, err := transaction.ExecContext(ctx, "UPDATE dbo.orders SET value=value+1 WHERE id=@p1", id)
			rollback := transaction.Rollback()
			if errors.Is(rollback, sql.ErrTxDone) {
				rollback = nil
			}
			return err, rollback
		}, task.WithContext(ctx), task.WithCancellationDrain()))
	}
	victims := 0
	for _, handle := range handles {
		result, err := handle.GetResult()
		if err != nil {
			return err
		}
		if result != nil {
			var serverError mssql.Error
			if !errors.As(result, &serverError) || serverError.Number != 1205 {
				return result
			}
			victims++
		}
	}
	if victims != 1 {
		return fmt.Errorf("expected one deadlock victim, got %d", victims)
	}
	return nil
}

func (w *labWorkload) close() error {
	var err error
	if w.db != nil {
		err = w.db.Close()
	}
	if w.created {
		if !strings.HasPrefix(w.name, "telltale_demo_") {
			return errors.Join(err, fmt.Errorf("refusing to drop non-demo database"), w.admin.Close())
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, dropErr := w.admin.ExecContext(ctx, "DROP DATABASE ["+w.name+"]")
		err = errors.Join(err, dropErr)
	}
	return errors.Join(err, w.admin.Close())
}
