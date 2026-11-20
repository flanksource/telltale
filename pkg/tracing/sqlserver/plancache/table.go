package plancache

import (
	"github.com/flanksource/clicky/api"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/telltale/pkg/tracing"
)

func (Sample) Columns() []api.ColumnDef {
	return query.TableColumnsFor[Sample]("cpuMs", "executions", "elapsedMs", "logicalReads", "sql", "database")
}
func (s Sample) Row() map[string]any     { return query.TableRowFor(s) }
func (s Sample) RowDetail() api.Textable { return s.PrettyFull() }

func topSpec() tracing.TopSpec {
	return tracing.TopSpec{Identity: []string{"planHandle"}, Order: query.Order{{Column: "cpuMs", Desc: true}, {Column: "planHandle"}}}
}
