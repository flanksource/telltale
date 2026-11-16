package processcapture

import (
	"github.com/flanksource/clicky/api"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/telltale/pkg/tracing"
)

func topSpec() tracing.TopSpec {
	return tracing.TopSpec{Profile: sampleProfile, Hierarchy: ownershipHierarchy, Metrics: []tracing.TopMetric{{Key: "cpuPercent", Label: "Process CPU", Unit: "percent", View: "flamegraph"}, {Key: "rssBytes", Label: "Process resident memory", Unit: "bytes", View: "treemap"}}, Result: "metrics", Identity: []string{"entity"}, Order: query.Order{{Column: "cpuPercent", Desc: true}, {Column: "entity"}}}
}
func (Identity) Columns() []api.ColumnDef {
	return query.TableColumnsFor[Identity]("pid", "name", "command")
}
func (r Identity) Row() map[string]any     { return query.TableRowFor(r) }
func (r Identity) RowDetail() api.Textable { return r.PrettyFull() }
func (MetricRow) Columns() []api.ColumnDef {
	return query.TableColumnsFor[MetricRow]("entity", "cpuPercent", "rssBytes", "openFiles", "diskReadBps", "diskWriteBps")
}
func (r MetricRow) Row() map[string]any     { return query.TableRowFor(r) }
func (r MetricRow) RowDetail() api.Textable { return r.PrettyFull() }
func (ProcessRow) Columns() []api.ColumnDef {
	return query.TableColumnsFor[ProcessRow]("pid", "name", "cpuPercent", "rssBytes", "command")
}
func (r ProcessRow) Row() map[string]any { return query.TableRowFor(r) }
