package traces

import (
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/sqlserver/deadlock"
	"github.com/flanksource/telltale/pkg/tracing/sqlserver/xevent"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

func init() {
	MustRegister(Registration{Name: "sql_deadlock", Title: "SQL Server deadlocks", Summary: "Read historical deadlocks or follow new deadlocks", Description: "Read system_health deadlock graphs over a historical server-time window or follow from now.", Seams: []target.Seam{target.Connection}, DefaultSeam: target.Connection, Armable: true, New: func(KindDeps) tracing.TracePlugin { return deadlock.Kind() }})
	MustRegister(Registration{Name: "sql_xevent", Title: "SQL Server XEvents", Summary: "Capture SQL Server Extended Events", Description: "Capture a managed XE session or attach read-only to system_health on a configured SQL Server connection.", Seams: []target.Seam{target.Connection}, DefaultSeam: target.Connection, Armable: true,
		Examples: []Example{{Args: "--connection connection://warehouse --session-name diagnostic --duration 30s", Says: "capture statements for thirty seconds"}},
		New:      func(KindDeps) tracing.TracePlugin { return xevent.Kind() }})
}
