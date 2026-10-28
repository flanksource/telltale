package plancache

import (
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/commons-db/recordstore/recordresults"
)

var byQueryView = recordresults.ResultView{
	Name: "by_query", Title: "Plan costs by query",
	Query: `SELECT queryHash, SUM(executions) AS executions, SUM(cpuMs) AS cpuMs,
		SUM(elapsedMs) AS elapsedMs, SUM(logicalReads) AS logicalReads,
		MAX(avgElapsedMs) AS avgElapsedMs, MIN(sql) AS sql,
		COUNT(DISTINCT planHandle) AS plans FROM stream_rows GROUP BY queryHash`,
	Columns: []query.ColumnDef{
		{Name: "queryHash", Label: "Query hash", Type: query.ColumnTypeString},
		{Name: "executions", Label: "Executions", Type: query.ColumnTypeNumber},
		{Name: "cpuMs", Label: "CPU", Type: query.ColumnTypeDuration, Unit: "ms"},
		{Name: "elapsedMs", Label: "Elapsed", Type: query.ColumnTypeDuration, Unit: "ms"},
		{Name: "logicalReads", Label: "Logical reads", Type: query.ColumnTypeNumber},
		{Name: "avgElapsedMs", Label: "Maximum average elapsed", Type: query.ColumnTypeDuration, Unit: "ms"},
		{Name: "sql", Label: "SQL", Type: query.ColumnTypeString},
		{Name: "plans", Label: "Distinct plans", Type: query.ColumnTypeNumber},
	},
	Order: query.Order{{Column: "cpuMs", Desc: true}, {Column: "queryHash", Unique: true}},
}

func (handler) Schema() recordresults.ResultType[Sample] {
	return recordresults.ResultType[Sample]{Title: "SQL Server plan cache samples", TimeColumn: "sampledAt", Follow: true,
		Indexes:       []recordstore.IndexDef{{Columns: []string{"planHandle", "sampledAt"}}},
		SearchColumns: []string{"sql", "object"}, Views: []recordresults.ResultView{byQueryView}}
}
