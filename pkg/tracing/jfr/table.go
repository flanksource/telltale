package jfr

import (
	"github.com/flanksource/clicky/api"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/telltale/pkg/tracing"
)

func (Sample) Columns() []api.ColumnDef {
	return query.TableColumnsFor[Sample]("event", "value", "thread", "frame")
}
func (s Sample) Row() map[string]any {
	row := query.TableRowFor(s)
	row["value"] = api.TableCell{Value: tracing.PrettySampleValue(s.Value, s.Unit), FilterValue: s.Value}
	return row
}
func (s Sample) RowDetail() api.Textable { return s.PrettyFull() }

func topSpec() tracing.TopSpec {
	return tracing.TopSpec{Stack: &tracing.TopStack{Column: "stack", MetricColumn: "event", Separator: ";"}, Identity: []string{"event", "thread", "stack"}, Order: query.Order{{Column: "value", Desc: true}}, Metrics: []tracing.TopMetric{
		{Key: "cpu", Label: "CPU samples", Unit: "samples", View: "flamegraph"},
		{Key: "alloc", Label: "Allocated bytes", Unit: "bytes", View: "treemap"},
		{Key: "wall", Label: "Wall samples", Unit: "samples", View: "flamegraph"},
		{Key: "lock", Label: "Lock time", Unit: "ns", View: "flamegraph"},
		{Key: "park", Label: "Park time", Unit: "ns", View: "flamegraph"},
	}}
}
