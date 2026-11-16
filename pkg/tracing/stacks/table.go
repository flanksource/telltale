package stacks

import (
	"github.com/flanksource/clicky/api"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/telltale/pkg/tracing"
)

func (Stack) Columns() []api.ColumnDef {
	return query.TableColumnsFor[Stack]("pid", "goroutineId", "waitMinutes", "state", "frames", "error")
}
func (s Stack) Row() map[string]any     { return query.TableRowFor(s) }
func (s Stack) RowDetail() api.Textable { return s.PrettyFull() }

func topSpec() tracing.TopSpec {
	return tracing.TopSpec{Identity: []string{"key", "goroutineId"}, GroupBy: []string{"key"}, Order: query.Order{{Column: "waitMinutes", Desc: true}, {Column: "goroutineId"}}}
}
