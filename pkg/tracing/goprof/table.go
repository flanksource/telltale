package goprof

import (
	"github.com/flanksource/clicky/api"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/telltale/pkg/tracing"
)

func (Sample) Columns() []api.ColumnDef {
	return query.TableColumnsFor[Sample]("pid", "profile", "sampleType", "value", "frames")
}
func (s Sample) Row() map[string]any {
	row := query.TableRowFor(s)
	row["value"] = api.TableCell{Value: tracing.PrettySampleValue(s.Value, s.Unit), FilterValue: s.Value}
	if len(s.Frames) > 0 {
		row["frames"] = api.Text{}.Append(s.Frames[0].Func, "font-mono")
	}
	return row
}
func (s Sample) RowDetail() api.Textable { return s.PrettyFull() }

func topSpec() tracing.TopSpec {
	return tracing.TopSpec{Stack: &tracing.TopStack{Column: "frames", MetricColumn: "sampleType", FrameKey: "func", LeafFirst: true}, Identity: []string{"stackHash", "sampleType"}, GroupBy: []string{"key", "profile"}, Order: query.Order{{Column: "value", Desc: true}}, Metrics: []tracing.TopMetric{
		{Key: "cpu", Label: "CPU time", Unit: "nanoseconds", View: "flamegraph"},
		{Key: "inuse_space", Label: "In-use bytes", Unit: "bytes", View: "treemap"},
		{Key: "alloc_space", Label: "Allocated bytes", Unit: "bytes", View: "treemap"},
		{Key: "inuse_objects", Label: "In-use objects", Unit: "count", View: "table"},
		{Key: "alloc_objects", Label: "Allocated objects", Unit: "count", View: "table"},
		{Key: "samples", Label: "Samples", Unit: "count", View: "flamegraph"},
		{Key: "goroutine", Label: "Goroutines", Unit: "count", View: "table"},
		{Key: "contentions", Label: "Contentions", Unit: "count", View: "table"},
		{Key: "delay", Label: "Delay", Unit: "nanoseconds", View: "flamegraph"},
	}}
}
