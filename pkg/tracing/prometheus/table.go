package prometheus

import (
	"github.com/flanksource/clicky/api"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/telltale/pkg/tracing"
)

func (Scrape) Columns() []api.ColumnDef {
	return query.TableColumnsFor[Scrape]("endpoint", "health", "durationMs", "samples", "error")
}
func (s Scrape) Row() map[string]any     { return query.TableRowFor(s) }
func (s Scrape) RowDetail() api.Textable { return s.PrettyFull() }

func topSpec() tracing.TopSpec {
	return tracing.TopSpec{PerSource: true, Identity: []string{"endpoint"}, Order: query.Order{{Column: "health"}, {Column: "durationMs", Desc: true}, {Column: "endpoint"}}}
}
