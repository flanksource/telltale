package history

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Window is the UTC time range a read covers, From inclusive and To
// exclusive. system_health stamps its events in UTC. A zero From leaves the
// window open at its start: everything the files still retain.
type Window struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// DatabaseScope is which databases' deadlocks the read keeps.
type DatabaseScope struct {
	// Names keeps deadlocks with a participant in one of these databases.
	Names []string
	// All keeps every database's deadlocks. With neither All nor Names, the
	// read keeps the connected database's.
	All bool
}

// ReadOptions bound one read of the system_health files.
type ReadOptions struct {
	Window    Window
	Databases DatabaseScope
}

// sqlTimeLayout is ISO 8601 without a zone, which CONVERT style 126 reads as
// a datetime2 — the type the event timestamp is compared as.
const sqlTimeLayout = "2006-01-02T15:04:05.0000000"

// readQuery is the one query a read issues. The window and the database are
// the predicates SQL applies, so events outside them are never decoded; the
// rest of a filter is applied to the decoded graphs.
//
// The window compares strings converted to datetime2 rather than bound
// time.Time values, which the driver sends as datetimeoffset and SQL Server
// would then compare after a conversion nobody asked for. A derived table
// rather than a CTE, and no ORDER BY: callers sort the decoded graphs.
func readQuery(source Source, options ReadOptions) (string, []any) {
	query := `SELECT e.event_data
FROM (
	SELECT f.event_data, CAST(f.event_data AS xml) AS event_xml
	FROM sys.fn_xe_file_target_read_file(?, NULL, NULL, NULL) AS f
	WHERE f.object_name = 'xml_deadlock_report'
) AS e
WHERE `
	args := []any{source.FilePattern}
	// A window with no start reads every graph the files still hold.
	if !options.Window.From.IsZero() {
		query += `e.event_xml.value('(event/@timestamp)[1]', 'datetime2') >= CONVERT(datetime2, ?, 126)
  AND `
		args = append(args, options.Window.From.UTC().Format(sqlTimeLayout))
	}
	query += `e.event_xml.value('(event/@timestamp)[1]', 'datetime2') < CONVERT(datetime2, ?, 126)`
	args = append(args, options.Window.To.UTC().Format(sqlTimeLayout))
	const participants = `
  AND EXISTS (
	SELECT 1 FROM e.event_xml.nodes('event/data[@name="xml_report"]/value/deadlock/process-list/process') AS p(process)
	WHERE p.process.value('@currentdbname', 'nvarchar(128)') %s)`
	switch {
	case options.Databases.All:
	case len(options.Databases.Names) > 0:
		query += fmt.Sprintf(participants, "IN (?)")
		args = append(args, options.Databases.Names)
	default:
		query += fmt.Sprintf(participants, "= DB_NAME()")
	}
	return query, args
}

// Read returns the xml_deadlock_report events within options, as their event
// XML.
func Read(ctx context.Context, db *gorm.DB, source Source, options ReadOptions) ([]string, error) {
	if !options.Window.From.Before(options.Window.To) {
		return nil, fmt.Errorf("deadlock window from %s is not before to %s",
			options.Window.From.Format(time.RFC3339), options.Window.To.Format(time.RFC3339))
	}
	query, args := readQuery(source, options)
	var rows []struct {
		EventData string `gorm:"column:event_data"`
	}
	if err := db.WithContext(ctx).Raw(query, args...).Scan(&rows).Error; err != nil {
		return nil, permissionHint(fmt.Errorf("read deadlock reports from %s: %w", source.FilePattern, err))
	}
	events := make([]string, len(rows))
	for i, row := range rows {
		events[i] = row.EventData
	}
	return events, nil
}

// oldestQuery is the time of the oldest deadlock report the files retain, read
// as the window predicate reads it, for the connected database.
const oldestQuery = `SELECT MIN(e.event_xml.value('(event/@timestamp)[1]', 'datetime2')) AS oldest
FROM (
	SELECT CAST(f.event_data AS xml) AS event_xml
	FROM sys.fn_xe_file_target_read_file(?, NULL, NULL, NULL) AS f
	WHERE f.object_name = 'xml_deadlock_report'
) AS e
WHERE EXISTS (
	SELECT 1 FROM e.event_xml.nodes('event/data[@name="xml_report"]/value/deadlock/process-list/process') AS p(process)
	WHERE p.process.value('@currentdbname', 'nvarchar(128)') = DB_NAME())`

// Oldest is the UTC time of the oldest deadlock report of the connected
// database the files retain, or nil when they hold none.
func Oldest(ctx context.Context, db *gorm.DB, source Source) (*time.Time, error) {
	var rows []struct {
		Oldest *time.Time `gorm:"column:oldest"`
	}
	if err := db.WithContext(ctx).Raw(oldestQuery, source.FilePattern).Scan(&rows).Error; err != nil {
		return nil, permissionHint(fmt.Errorf("read the oldest deadlock report in %s: %w", source.FilePattern, err))
	}
	if len(rows) != 1 {
		return nil, fmt.Errorf("the oldest deadlock report query returned %d rows, want 1", len(rows))
	}
	if rows[0].Oldest == nil {
		return nil, nil
	}
	oldest := time.Date(rows[0].Oldest.Year(), rows[0].Oldest.Month(), rows[0].Oldest.Day(),
		rows[0].Oldest.Hour(), rows[0].Oldest.Minute(), rows[0].Oldest.Second(), rows[0].Oldest.Nanosecond(), time.UTC)
	return &oldest, nil
}

// ScopeDatabases decides how much of a database filter SQL can apply. An empty
// filter scopes to the connected database. Literal names are pushed down as
// they are. A wildcard or an exclusion cannot be expressed as the IN list the
// query uses, so the read keeps every database and the filter is applied to
// the decoded graphs instead.
func ScopeDatabases(values []string) DatabaseScope {
	var names []string
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if strings.ContainsAny(part, "*!") {
				return DatabaseScope{All: true}
			}
			names = append(names, part)
		}
	}
	return DatabaseScope{Names: names}
}
