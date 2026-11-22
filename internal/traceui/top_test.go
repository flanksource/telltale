package traceui

import (
	"context"

	"github.com/charmbracelet/x/ansi"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/commons-db/recordstore/sampleframe"
	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/traces/read"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/httptop"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("sample navigation", func() {
	It("renders HTTP Top direction arrows without rewriting recorded directions", func() {
		kinds, err := traces.Kinds()
		Expect(err).NotTo(HaveOccurred())
		sample := query.Result{Rows: []query.Row{
			{"direction": "incoming", "requestId": "one", "source": "fixture"},
			{"direction": "outgoing", "requestId": "two", "source": "fixture"},
		}}
		display, err := sample.DisplayTable([]query.ColumnDef{{Name: "direction", Label: "Direction"}})
		Expect(err).NotTo(HaveOccurred())
		top := &sampledTab{ref: read.Ref{TraceRef: tracing.TraceRef{Kind: httptop.Kind}, Runtime: &tracing.Runtime{Kinds: kinds}}}
		Expect(top.receive(pageMsg{top: &read.TopResult{Rows: sample.Rows, Display: display}})).To(Succeed())
		model := Model{tabs: []tab{{name: httptop.Kind, top: top}}, width: 80, height: 20}
		view := ansi.Strip(model.sampleView().Content)
		Expect(view).To(And(ContainSubstring("Direction"), ContainSubstring("←"), ContainSubstring("→")))
		Expect(top.result.Rows).To(Equal([]query.Row{
			{"direction": "incoming", "requestId": "one", "source": "fixture"},
			{"direction": "outgoing", "requestId": "two", "source": "fixture"},
		}))
	})

	It("keeps historical navigation in the chosen source and freezes its sample", func() {
		sampled := &sampledTab{source: "first", frame: "a2", frames: []sampleframe.Frame{{ID: "a1", Source: "first"}, {ID: "b1", Source: "second"}, {ID: "a2", Source: "first"}}}
		model := Model{ctx: context.Background(), tabs: []tab{{top: sampled}}}
		next, _, handled := model.sampleKey("p")
		Expect(handled).To(BeTrue())
		Expect(next.(Model).tabs[0].top.frame).To(Equal("a1"))
		Expect(next.(Model).tabs[0].paused).To(BeTrue())
	})
	It("cycles available metrics and keeps detail closed when changing the metric", func() {
		sampled := &sampledTab{metric: "cpu", result: &read.TopResult{Metrics: []tracing.TopMetric{{Key: "cpu"}, {Key: "memory"}}}}
		sampled.table.Detail = true
		model := Model{ctx: context.Background(), tabs: []tab{{top: sampled}}}
		next, _, _ := model.sampleKey("m")
		Expect(next.(Model).tabs[0].top.metric).To(Equal("memory"))
		Expect(next.(Model).tabs[0].top.table.Detail).To(BeFalse())
	})
})
