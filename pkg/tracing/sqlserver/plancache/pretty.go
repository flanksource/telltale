package plancache

import (
	"fmt"
	"time"

	"github.com/flanksource/clicky"
	"github.com/flanksource/clicky/api"
	"github.com/flanksource/commons-db/query/sqltext"
)

func (r Sample) Pretty() api.Text {
	duration := time.Duration(r.AvgElapsedMs * float64(time.Millisecond))
	t := clicky.Text(r.Database, "text-blue-500").Space().Append(fmt.Sprintf("%d executions", r.Executions), "text-muted").
		Space().Append("avg ", "text-muted").Add(api.Human(duration, sqltext.DurationStyle(duration))).Space().Append(r.SQL, "font-mono max-w-[200ch]")
	if r.Recompiled {
		t = t.Space().Append("recompiled", "text-yellow-500")
	}
	if r.Evicted {
		t = t.Space().Append("evicted", "text-yellow-500")
	}
	return t
}

func (r Sample) PrettyFull() api.Textable {
	return r.Pretty().NewLine().Add(clicky.CodeBlock("sql", r.SQL)).NewLine().Add(clicky.Map(map[string]any{
		"CPU":     api.Human(time.Duration(r.CPUMs * float64(time.Millisecond))),
		"Elapsed": api.Human(time.Duration(r.ElapsedMs * float64(time.Millisecond))),
		"Reads":   r.LogicalReads, "Physical reads": r.PhysicalReads, "Writes": r.Writes, "Rows": r.Rows,
		"Plan": r.PlanHandle, "Query hash": r.QueryHash, "Size": api.HumanizeBytes(r.SizeBytes),
	}))
}
