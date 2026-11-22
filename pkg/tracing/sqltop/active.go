package sqltop

import (
	"fmt"
	"time"

	"github.com/flanksource/clicky"
	"github.com/flanksource/clicky/api"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/commons-db/query/sqlactivity"
	"github.com/flanksource/commons-db/recordstore/recordresults"
)

type ActiveSample struct {
	Source               string    `json:"source" filter:"terms"`
	OperationID          string    `json:"operationId" filter:"exact"`
	BlockedByOperationID string    `json:"blockedByOperationId,omitempty" filter:"exact"`
	SampledAt            time.Time `json:"sampledAt" filter:"time"`
	StartedAt            time.Time `json:"startedAt" filter:"time"`
	ElapsedMs            float64   `json:"elapsedMs" filter:"duration"`
	API                  string    `json:"api" filter:"terms"`
	Phase                string    `json:"phase,omitempty" filter:"terms"`
	Database             string    `json:"database,omitempty" filter:"terms"`
	Engine               string    `json:"engine,omitempty" filter:"terms"`
	SQL                  string    `json:"sql,omitempty" filter:"text"`
	QueryFingerprint     string    `json:"queryFingerprint,omitempty" filter:"exact"`
	SQLTruncated         bool      `json:"sqlTruncated,omitempty"`
	SQLUnavailableReason string    `json:"sqlUnavailableReason,omitempty" filter:"terms"`
	Connection           string    `json:"connection,omitempty" filter:"terms"`
	TraceID              string    `json:"traceId,omitempty" filter:"exact"`
	SpanID               string    `json:"spanId,omitempty" filter:"exact"`
	BatchSize            *int64    `json:"batchSize,omitempty"`
	Command              string    `json:"command,omitempty" filter:"terms"`
	sqlactivity.SQLServerProgress
}

func ActiveResult() recordresults.ResultType[ActiveSample] {
	return recordresults.ResultType[ActiveSample]{Kind: "sql_top_active", Title: "Active SQL operations", TimeColumn: "sampledAt", Follow: true, SearchColumns: []string{"sql", "sqlUnavailableReason", "database"}}
}

func (ActiveSample) Columns() []api.ColumnDef {
	return query.TableColumnsFor[ActiveSample]("source", "operationId", "blockedByOperationId", "sampledAt", "startedAt", "elapsedMs", "percentComplete",
		"api", "phase", "database", "engine", "sql", "queryFingerprint", "sqlTruncated", "sqlUnavailableReason", "connection", "traceId", "spanId", "batchSize")
}

func (s ActiveSample) Row() map[string]any {
	row := query.TableRowFor(s)
	if s.Engine == EngineSQLServer && s.SQL != "" {
		row["sql"] = api.TableCell{Value: clicky.CodeBlock("sql", s.SQL), FilterValue: s.SQL}
	}
	return row
}

func (s ActiveSample) RowDetail() api.Textable {
	progress := s.SQLServerProgress.Detail()
	if progress == nil && (s.Engine != EngineSQLServer || s.SQL == "") {
		return nil
	}
	detail := api.Text{}
	if s.SQL != "" {
		detail = detail.Add(clicky.CodeBlock("sql", s.SQL))
	}
	if progress != nil {
		if s.SQL != "" {
			detail = detail.NewLine()
		}
		detail = detail.Add(clicky.Map(map[string]string{"Command": s.Command})).NewLine().Add(progress)
	}
	return detail
}

func projectDatabase(source string, rows []Sample) []ActiveSample {
	projected := make([]ActiveSample, len(rows))
	for i, row := range rows {
		projected[i] = ActiveSample{Source: source, OperationID: fmt.Sprintf("%d/%d", row.SessionID, row.RequestID), SampledAt: row.SampledAt, StartedAt: row.StartedAt, ElapsedMs: row.ElapsedMs, API: "database", Database: row.Database, Engine: row.Engine, SQL: row.SQL, Connection: source}
		if row.Engine == EngineSQLServer && row.SQLServerProgress != (sqlactivity.SQLServerProgress{}) {
			projected[i].Command = row.Command
			projected[i].SQLServerProgress = row.SQLServerProgress
		}
		if row.BlockedBy != 0 {
			for _, blocker := range rows {
				if blocker.SessionID == row.BlockedBy {
					projected[i].BlockedByOperationID = fmt.Sprintf("%d/%d", blocker.SessionID, blocker.RequestID)
					break
				}
			}
		}
	}
	return projected
}
