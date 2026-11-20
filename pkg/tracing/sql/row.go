// A captured statement as its stream stores it, and as its result profile
// renders it: the columns, and the cells styled as an xevent's are.

package sql

import (
	"time"

	"github.com/flanksource/clicky/api"
	"github.com/flanksource/commons-db/connection"
	"github.com/flanksource/commons-db/query/sqltext"
	"github.com/flanksource/telltale/pkg/tracing"
)

// RowKind is the record kind a capture's stream holds, and the kind a host
// registers Kind as: one StatementRow per statement.
const RowKind = "sql"

// StatementRow is one statement as a capture stores it. Op and Tables are
// derived from its text as xetrace classifies a captured event's, and
// RepeatKey keys the statements that differ only by their values.
type StatementRow struct {
	tracing.Truncation
	Observation
	StartedAt  time.Time `json:"startedAt" pretty:"label=Started" sort:"startedAt"`
	Connection string    `json:"connection" filter:"terms"`
	Driver     string    `json:"driver" filter:"terms"`
	Op         string    `json:"op" pretty:"label=Op" filter:"terms"`
	Tables     []string  `json:"tables" filter:"terms"`
	SQL        string    `json:"sql" pretty:"label=SQL" filter:"text"`
	RepeatKey  string    `json:"repeatKey" pretty:"hide" filter:"exact"`
	Args       []any     `json:"args,omitempty" pretty:"hide"`
	DurationMs float64   `json:"durationMs" pretty:"label=Duration,type=duration,unit=ms" sort:"durationMs"`
	// Rows is the observed affected or returned row count, or -1 when unknown.
	Rows     int64             `json:"rows" sort:"rows"`
	Error    string            `json:"error,omitempty" filter:"text"`
	Resource map[string]string `json:"resource,omitempty"`
}

type Observation struct {
	OperationID          string `json:"operationId,omitempty" filter:"exact"`
	TraceID              string `json:"traceId,omitempty" filter:"exact"`
	SpanID               string `json:"spanId,omitempty" filter:"exact"`
	API                  string `json:"api,omitempty" filter:"terms"`
	Database             string `json:"database,omitempty" filter:"terms"`
	Engine               string `json:"engine,omitempty" filter:"terms"`
	Phase                string `json:"phase,omitempty" filter:"terms"`
	Incomplete           bool   `json:"incomplete,omitempty" filter:"terms"`
	Completion           string `json:"completion,omitempty" filter:"terms"`
	BatchSize            *int64 `json:"batchSize,omitempty"`
	SQLUnavailableReason string `json:"sqlUnavailableReason,omitempty" filter:"terms"`
}

// FromStatement is the row a capture stores for statement. A statement naming
// no table has an empty list of them, never none.
func FromStatement(statement connection.Statement) StatementRow {
	tables := sqltext.ExtractTables(statement.SQL)
	if tables == nil {
		tables = []string{}
	}
	return StatementRow{
		StartedAt: statement.StartedAt.UTC(), Connection: statement.Connection, Driver: statement.Driver,
		Op: string(sqltext.ClassifyStatement(statement.SQL)), Tables: tables,
		SQL: statement.SQL, RepeatKey: RepeatKey(statement.SQL), Args: statement.Args,
		DurationMs: float64(statement.Duration.Microseconds()) / 1000,
		Rows:       statement.Rows, Error: statement.Error,
	}
}

func (StatementRow) Columns() []api.ColumnDef {
	return []api.ColumnDef{
		api.Column("startedAt").Label("Time").Kind("timestamp").Build(),
		api.Column("sql").Label("Statement").MinWidthPixels(360).MaxWidthPixels(720).Build(),
		api.Column("op").Label("Op").Build(),
		api.Column("tables").Label("Tables").Kind("tags").Build(),
		api.Column("durationMs").Label("Duration").Style("text-right").Type("duration").Unit("ms").FilterKey("filter.durationMs").Build(),
		api.Column("rows").Label("Rows").Style("text-right").Type("number").FilterKey("filter.rows").Build(),
		api.Column("connection").Label("Connection").DefaultHidden().Build(),
		api.Column("driver").Label("Driver").DefaultHidden().Build(),
		api.Column("error").Hidden().Build(),
		api.Column("args").Kind("json").Hidden().Build(),
		api.Column("repeatKey").Hidden().Build(),
		api.Column("truncated").Hidden().Build(),
		api.Column("operationId").Hidden().Build(),
		api.Column("traceId").Hidden().Build(),
		api.Column("spanId").Hidden().Build(),
		api.Column("api").DefaultHidden().Build(),
		api.Column("batchSize").DefaultHidden().Build(),
		api.Column("sqlUnavailableReason").DefaultHidden().Build(),
		api.Column("phase").DefaultHidden().Build(),
		api.Column("incomplete").DefaultHidden().Build(),
		api.Column("completion").DefaultHidden().Build(),
		api.Column("database").DefaultHidden().Build(),
		api.Column("engine").DefaultHidden().Build(),
	}
}

func (r StatementRow) Row() map[string]any {
	duration := milliseconds(r.DurationMs)
	rows := api.Text{}
	if r.Rows >= 0 {
		rows = api.HumanNumber(r.Rows, "text-muted")
	}
	return map[string]any{
		"startedAt":  r.StartedAt,
		"sql":        api.TableCell{Value: r.statementCell(), FilterValue: r.SQL},
		"op":         r.Op,
		"tables":     r.Tables,
		"durationMs": api.TableCell{Value: api.Human(duration, sqltext.DurationStyle(duration)), FilterValue: r.DurationMs},
		"rows":       api.TableCell{Value: rows, FilterValue: r.Rows},
		"connection": r.Connection, "driver": r.Driver, "error": r.Error,
		"args": r.Args, "repeatKey": r.RepeatKey, "truncated": r.Truncated,
		"operationId": r.OperationID, "traceId": r.TraceID, "spanId": r.SpanID,
		"api": r.API, "batchSize": r.BatchSize, "sqlUnavailableReason": r.SQLUnavailableReason,
		"phase": r.Phase, "incomplete": r.Incomplete, "completion": r.Completion,
		"database": r.Database, "engine": r.Engine,
	}
}

// statementCell is the statement's SQL, and for a statement that failed, its
// error in red beneath it.
func (r StatementRow) statementCell() api.Textable {
	if r.SQLUnavailableReason != "" {
		return api.Text{}.AddText("SQL unavailable: "+r.SQLUnavailableReason, "text-muted")
	}
	code := api.CodeBlock("text/x-sql", r.SQL)
	if r.Error == "" {
		return code
	}
	return api.Text{}.Add(code).NewLine().AddText(r.Error, "text-red-500")
}
