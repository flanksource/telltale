package plancache

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

const pollMarker = "/* telltale:sql_plancache */"

type snapshotOptions struct {
	Filter Filter
	Since  time.Time
}

type cacheSnapshot struct {
	ServerTime time.Time
	Handles    []string
	Plans      []CachedPlan
}

func readSnapshot(ctx context.Context, db *gorm.DB, options snapshotOptions) (result cacheSnapshot, err error) {
	q, err := snapshotQuery(db.WithContext(ctx), options)
	if err != nil {
		return result, err
	}
	var plans []cachedPlanRow
	tx := q.Session(&gorm.Session{DryRun: true}).Select(selectList).Find(&plans)
	if tx.Error != nil {
		return result, tx.Error
	}
	statement := pollMarker + `
DECLARE @sampled_at datetime = SYSDATETIME();
SELECT @sampled_at AS sampled_at;
SELECT cp.plan_handle FROM sys.dm_exec_cached_plans AS cp;
` + tx.Statement.SQL.String() + ";"
	args := make([]any, len(tx.Statement.Vars))
	for i, value := range tx.Statement.Vars {
		args[i] = sql.Named(fmt.Sprintf("p%d", i+1), value)
	}
	pool, err := db.DB()
	if err != nil {
		return result, err
	}
	rows, err := pool.QueryContext(ctx, statement, args...)
	if err != nil {
		return result, fmt.Errorf("sample plan cache: %w", err)
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	return scanSnapshot(db, rows)
}

func snapshotQuery(db *gorm.DB, options snapshotOptions) (*gorm.DB, error) {
	q, err := baseQuery(db, options.Filter)
	if err != nil {
		return nil, err
	}
	if !options.Since.IsZero() {
		q = q.Where("qs.last_execution_time >= ?", options.Since)
	}
	return q, nil
}

func scanSnapshot(db *gorm.DB, rows *sql.Rows) (cacheSnapshot, error) {
	var result cacheSnapshot
	if !rows.Next() {
		return result, errors.Join(fmt.Errorf("plan cache snapshot has no server timestamp"), rows.Err())
	}
	if err := rows.Scan(&result.ServerTime); err != nil {
		return result, fmt.Errorf("read plan cache server timestamp: %w", err)
	}
	if rows.Next() {
		return result, fmt.Errorf("plan cache snapshot has multiple server timestamps")
	}
	if !rows.NextResultSet() {
		return result, errors.Join(fmt.Errorf("plan cache snapshot has no membership result set"), rows.Err())
	}
	// All cache handles are needed to distinguish quiet plans from evictions.
	// This set deliberately reads neither SQL text nor query statistics.
	for rows.Next() {
		var handle []byte
		if err := rows.Scan(&handle); err != nil {
			return result, fmt.Errorf("read plan cache membership: %w", err)
		}
		result.Handles = append(result.Handles, EncodeHandle(handle))
	}
	if err := rows.Err(); err != nil {
		return result, fmt.Errorf("read plan cache membership: %w", err)
	}
	if !rows.NextResultSet() {
		return result, errors.Join(fmt.Errorf("plan cache snapshot has no active plans result set"), rows.Err())
	}
	for rows.Next() {
		var row cachedPlanRow
		if err := db.ScanRows(rows, &row); err != nil {
			return result, fmt.Errorf("read active plan: %w", err)
		}
		result.Plans = append(result.Plans, row.toCachedPlan())
	}
	if err := rows.Err(); err != nil {
		return result, fmt.Errorf("read active plans: %w", err)
	}
	if rows.NextResultSet() {
		return result, fmt.Errorf("plan cache snapshot has an unexpected result set")
	}
	return result, rows.Err()
}
