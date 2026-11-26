package sqltop

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

const sqlServerInstanceCPU = `
SELECT ms_ticks, sqlserver_start_time_ms_ticks, process_user_time_ms + process_kernel_time_ms, cpu_count
FROM sys.dm_os_sys_info`

const sqlServerIOCounters = `
SELECT SYSUTCDATETIME(), info.sqlserver_start_time, files.database_id, files.file_id, files.file_handle,
       files.num_of_reads, files.num_of_writes
FROM sys.dm_io_virtual_file_stats(NULL, NULL) AS files
CROSS JOIN sys.dm_os_sys_info AS info`

const sqlServerPageLife = `
SELECT MAX(cntr_value)
FROM sys.dm_os_performance_counters
WHERE RTRIM(object_name) LIKE N'%:Buffer Manager' AND counter_name = N'Page life expectancy' AND instance_name = N''`

type sqlServerMetrics struct{ pool *sql.DB }

func (m sqlServerMetrics) read(ctx context.Context) (reading serverReading, err error) {
	var cpu sqlServerCPUCounters
	err = m.pool.QueryRowContext(ctx, sqlServerInstanceCPU).Scan(&cpu.at, &cpu.startedAt, &cpu.processMS, &cpu.cpus)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		reading.unavailableFor("No SQL Server CPU counters reported", cpuPercent)
	case sqlServerPermissionDenied(err):
		reading.unavailableFor("Requires access to sys.dm_os_sys_info", cpuPercent)
	case err != nil:
		return reading, fmt.Errorf("read SQL Server CPU: %w", err)
	default:
		reading.sqlServerCPU = &cpu
	}
	if err = m.readIO(ctx, &reading.io); sqlServerPermissionDenied(err) {
		reading.io = ioCounters{}
		reading.unavailableFor("Requires access to SQL Server file I/O statistics", readIOPS, writeIOPS)
	} else if err != nil {
		return reading, fmt.Errorf("read SQL Server file I/O: %w", err)
	} else if reading.io.files == nil {
		reading.unavailableFor("No file I/O counters reported", readIOPS, writeIOPS)
	}
	err = m.pool.QueryRowContext(ctx, sqlServerPageLife).Scan(&reading.pageLifeSeconds)
	switch {
	case sqlServerPermissionDenied(err):
		reading.unavailableFor("Requires access to sys.dm_os_performance_counters", pageLifeSeconds)
	case err != nil:
		return reading, fmt.Errorf("read SQL Server page life expectancy: %w", err)
	case reading.pageLifeSeconds == nil:
		reading.unavailableFor("No Buffer Manager counter reported", pageLifeSeconds)
	}
	return reading, nil
}

func (m sqlServerMetrics) readIO(ctx context.Context, counters *ioCounters) (err error) {
	rows, err := m.pool.QueryContext(ctx, sqlServerIOCounters)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var file fileKey
		var count operations
		if err := rows.Scan(&counters.at, &counters.reset, &file.database, &file.file, &file.handle, &count.reads, &count.writes); err != nil {
			return err
		}
		if counters.files == nil {
			counters.files = make(map[fileKey]operations)
		}
		counters.files[file] = count
	}
	return rows.Err()
}

func sqlServerPermissionDenied(err error) bool {
	var server interface{ SQLErrorNumber() int32 }
	if !errors.As(err, &server) {
		return false
	}
	switch server.SQLErrorNumber() {
	case 229, 297, 300:
		return true
	default:
		return false
	}
}
