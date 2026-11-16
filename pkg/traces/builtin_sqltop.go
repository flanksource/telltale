package traces

import (
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/sqltop"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

func init() {
	MustRegister(Registration{Name: "sql_top", Title: "SQL top", Summary: "Sample the queries a Postgres or SQL Server is running",
		Description: "Poll pg_stat_activity or sys.dm_exec_requests on a configured connection and record each running request, longest first: " +
			"its session, wait, blocker, elapsed time and SQL. Sessions holding an open transaction are always recorded; --idle adds idle sessions.",
		Seams: []target.Seam{target.Connection}, DefaultSeam: target.Connection, Armable: true, Checkpoint: map[string]any{},
		Examples: []Example{
			{Args: "--connection connection://orders --duration 2m --poll 2s", Says: "watch what a Postgres runs for two minutes"},
			{Args: "--connection connection://warehouse --databases 'sales*' --min-elapsed 500ms --top 20", Says: "record the twenty longest SQL Server requests over half a second"},
		},
		New: func(KindDeps) tracing.TracePlugin { return sqltop.Kind() }})
}
