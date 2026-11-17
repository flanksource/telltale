package top

import (
	"fmt"
	"github.com/flanksource/clicky/api"
	"github.com/flanksource/telltale/pkg/tracing/jvm/jvmruntime"
)

type Thread struct{ jvmruntime.Thread }

func (Thread) Columns() []api.ColumnDef {
	return []api.ColumnDef{
		api.Column("id").Label("ID").Build(), api.Column("cpu").Label("CPU%/core").Build(),
		api.Column("state").Label("State").Build(), api.Column("name").Label("Thread").MaxWidth(50).Build(),
		api.Column("cpuTime").Label("CPU ms").Build(), api.Column("blocked").Label("Blocked").Build(),
		api.Column("waited").Label("Waited").Build(), api.Column("owner").Label("Lock owner").Build(),
	}
}
func (t Thread) Row() map[string]any {
	cpu, cpuTime, owner := "unavailable", "unavailable", "—"
	if t.CPUPercent != nil {
		cpu = fmt.Sprintf("%.1f", *t.CPUPercent)
	}
	if t.CPUTimeMs != nil {
		cpuTime = fmt.Sprint(*t.CPUTimeMs)
	}
	if t.LockOwnerName != nil {
		owner = *t.LockOwnerName
	}
	return map[string]any{"id": t.ID, "cpu": cpu, "state": t.State, "name": t.Name, "cpuTime": cpuTime, "blocked": fmt.Sprint(t.BlockedCount), "waited": fmt.Sprint(t.WaitedCount), "owner": owner}
}
func Table(rows []jvmruntime.Thread) api.TextTable {
	display := make([]Thread, len(rows))
	for i, row := range rows {
		display[i] = Thread{row}
	}
	return api.NewTableFrom(display)
}
