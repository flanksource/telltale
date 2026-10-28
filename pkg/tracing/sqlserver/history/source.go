package history

import (
	"context"
	"fmt"
	"strings"

	"github.com/flanksource/telltale/pkg/tracing/sqlserver/xetrace"
	"gorm.io/gorm"
)

// Source is where system_health keeps its deadlock reports on this instance.
type Source = xetrace.SystemHealthSource

// ResolveSource finds system_health's event files. It fails when the session
// is not running with a file target, which is the case on Azure SQL Database
// and on an instance where a DBA stopped or altered the session: there is then
// no record of past deadlocks to read.
func ResolveSource(ctx context.Context, db *gorm.DB) (Source, error) {
	pool, err := db.DB()
	if err != nil {
		return Source{}, fmt.Errorf("resolve connection pool: %w", err)
	}
	conn, err := pool.Conn(ctx)
	if err != nil {
		return Source{}, permissionHint(err)
	}
	defer func() { _ = conn.Close() }()
	source, err := xetrace.ResolveSystemHealthSource(ctx, conn)
	if err != nil {
		return Source{}, permissionHint(err)
	}
	return source, nil
}

// rolloverSuffix is what SQL Server appends to an event file's base name as
// it rolls it over: _<file number>_<timestamp>.xel.
// FilePattern turns the file a session is writing now into the wildcard that
// matches all of its rollover files, in the same directory. The directory
// separator is the server's — \ on Windows, / on Linux — so it is kept as is.
func FilePattern(currentFile string) (string, error) {
	return xetrace.SystemHealthFilePattern(currentFile)
}

const deadlockCounterQuery = `SELECT CAST(pc.cntr_value AS bigint) AS value
FROM sys.dm_os_performance_counters AS pc
WHERE pc.object_name LIKE '%:Locks%'
  AND RTRIM(pc.counter_name) = 'Number of Deadlocks/sec'
  AND RTRIM(pc.instance_name) = '_Total'`

// DeadlockCounter reads the instance's running count of deadlocks resolved.
// Despite its /sec name the counter is cumulative, so the difference between
// two reads is the deadlocks resolved in between, across every database.
func DeadlockCounter(ctx context.Context, db *gorm.DB) (int64, error) {
	var rows []struct {
		Value int64 `gorm:"column:value"`
	}
	if err := db.WithContext(ctx).Raw(deadlockCounterQuery).Scan(&rows).Error; err != nil {
		return 0, permissionHint(fmt.Errorf("read the Number of Deadlocks/sec counter: %w", err))
	}
	if len(rows) != 1 {
		return 0, fmt.Errorf("the Number of Deadlocks/sec _Total counter returned %d rows, want 1", len(rows))
	}
	return rows[0].Value, nil
}

// permissionHint names the grant a denied DMV read needs. Without it the
// driver's message says only that VIEW SERVER STATE was denied, not which
// grant fixes it on a 2022 instance, where the narrower permission suffices.
func permissionHint(err error) error {
	message := strings.ToUpper(err.Error())
	if strings.Contains(message, "VIEW SERVER STATE") || strings.Contains(message, "VIEW SERVER PERFORMANCE STATE") ||
		strings.Contains(message, "PERMISSION") {
		return fmt.Errorf("%w — reading deadlock graphs needs VIEW SERVER STATE (SQL Server 2019 and older) "+
			"or VIEW SERVER PERFORMANCE STATE (2022 and newer) for this login", err)
	}
	return err
}
