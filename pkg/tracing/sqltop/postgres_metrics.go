package sqltop

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

const postgresMetricSources = `
SELECT pg_catalog.to_regclass('pg_catalog.pg_stat_io') IS NOT NULL,
       COALESCE(max(qualified) FILTER (WHERE extname = 'pg_proctab' AND proname = 'pg_cputime'), ''),
       COALESCE(max(qualified) FILTER (WHERE extname = 'pg_buffercache' AND proname = 'pg_buffercache_summary'), '')
FROM (
    SELECT e.extname, p.proname, pg_catalog.format('%I.%I', n.nspname, p.proname) AS qualified
    FROM pg_catalog.pg_extension e
    JOIN pg_catalog.pg_depend d ON d.refclassid = 'pg_catalog.pg_extension'::regclass
        AND d.refobjid = e.oid AND d.deptype = 'e' AND d.classid = 'pg_catalog.pg_proc'::regclass
    JOIN pg_catalog.pg_proc p ON p.oid = d.objid AND p.pronargs = 0
    JOIN pg_catalog.pg_namespace n ON n.oid = p.pronamespace
    WHERE e.extname IN ('pg_proctab', 'pg_buffercache')
) AS functions`

const postgresIOCounters = `
SELECT pg_catalog.clock_timestamp(),
       GREATEST(max(stats_reset), pg_catalog.pg_postmaster_start_time()),
       sum(reads)::bigint, sum(writes)::bigint
FROM pg_catalog.pg_stat_io`

type postgresMetrics struct {
	pool           *sql.DB
	hasIO          bool
	cpuFunction    string
	bufferFunction string
}

func newPostgresMetrics(ctx context.Context, pool *sql.DB) (*postgresMetrics, error) {
	m := &postgresMetrics{pool: pool}
	if err := pool.QueryRowContext(ctx, postgresMetricSources).Scan(&m.hasIO, &m.cpuFunction, &m.bufferFunction); err != nil {
		return nil, fmt.Errorf("discover Postgres server metrics: %w", err)
	}
	return m, nil
}

func (m postgresMetrics) read(ctx context.Context) (serverReading, error) {
	var reading serverReading
	if err := m.readCPU(ctx, &reading); err != nil {
		return reading, fmt.Errorf("read pg_cputime: %w", err)
	}
	if err := m.readIO(ctx, &reading); err != nil {
		return reading, fmt.Errorf("read pg_stat_io: %w", err)
	}
	if err := m.readBuffers(ctx, &reading); err != nil {
		return reading, fmt.Errorf("read pg_buffercache_summary: %w", err)
	}
	return reading, nil
}

func (m postgresMetrics) readCPU(ctx context.Context, reading *serverReading) error {
	if m.cpuFunction == "" {
		reading.unavailableFor("Requires pg_proctab", cpuPercent)
		return nil
	}
	var ticks [5]int64
	err := m.pool.QueryRowContext(ctx, `SELECT pg_catalog.pg_postmaster_start_time(), "user", nice, system, idle, iowait FROM `+m.cpuFunction+"()").
		Scan(&reading.postgresStartedAt, &ticks[0], &ticks[1], &ticks[2], &ticks[3], &ticks[4])
	if postgresPermissionDenied(err) {
		reading.unavailableFor("Requires access to pg_cputime()", cpuPercent)
		return nil
	}
	if err == nil {
		reading.cpuTicks = &ticks
	}
	return err
}

func (m postgresMetrics) readIO(ctx context.Context, reading *serverReading) error {
	if !m.hasIO {
		reading.unavailableFor("Requires PostgreSQL 16 or later", readIOPS, writeIOPS)
		return nil
	}
	var totals operations
	err := m.pool.QueryRowContext(ctx, postgresIOCounters).Scan(&reading.io.at, &reading.io.reset, &totals.reads, &totals.writes)
	if postgresPermissionDenied(err) {
		reading.unavailableFor("Requires access to pg_stat_io", readIOPS, writeIOPS)
		return nil
	}
	if err == nil {
		reading.io.files = map[fileKey]operations{{}: totals}
	}
	return err
}

func (m postgresMetrics) readBuffers(ctx context.Context, reading *serverReading) error {
	if m.bufferFunction == "" {
		reading.unavailableFor("Requires pg_buffercache with its summary function", bufferUsedPercent)
		return nil
	}
	err := m.pool.QueryRowContext(ctx, "SELECT 100.0 * buffers_used / (buffers_used + buffers_unused) FROM "+m.bufferFunction+"()").
		Scan(&reading.bufferUsagePercent)
	if postgresPermissionDenied(err) {
		reading.unavailableFor("Requires access to pg_buffercache_summary()", bufferUsedPercent)
		return nil
	}
	return err
}

func postgresPermissionDenied(err error) bool {
	var state interface{ SQLState() string }
	return errors.As(err, &state) && state.SQLState() == "42501"
}
