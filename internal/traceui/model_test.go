package traceui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"github.com/flanksource/telltale/pkg/traces/read"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/processcapture"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"testing"
	"time"
)

func TestTraceUI(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Trace UI") }

var _ = Describe("record model", func() {
	runtime := &tracing.Runtime{Kinds: tracing.NewKinds()}
	It("keeps enabled empty kinds visible and uses an alternate screen", func() {
		model := New(context.Background(), Options{Refs: []read.Ref{{Runtime: runtime, TraceRef: tracing.TraceRef{Kind: "sql", Stream: "s"}}, {Runtime: runtime, TraceRef: tracing.TraceRef{Kind: "http", Stream: "h"}}}})
		Expect(model.tabs).To(HaveLen(5))
		Expect(model.tabs[0].name).To(Equal("All events"))
		Expect(model.tabs[0].refs).To(HaveLen(2))
		Expect(model.options.Sort).To(Equal("timestamp"))
		Expect(model.View().AltScreen).To(BeTrue())
	})
	It("preserves selection and pause independently across tabs", func() {
		model := New(context.Background(), Options{Refs: []read.Ref{{Runtime: runtime, TraceRef: tracing.TraceRef{Kind: "http", Stream: "h"}}, {Runtime: runtime, TraceRef: tracing.TraceRef{Kind: "sql", Stream: "s"}}}})
		model.tabs[0].selected = 3
		model.tabs[0].paused = true
		updated, _ := model.Update(tea.KeyPressMsg{Code: tea.KeyTab})
		model = updated.(Model)
		Expect(model.active).To(Equal(1))
		Expect(model.tabs[1].selected).To(BeZero())
		Expect(model.tabs[0].paused).To(BeTrue())
	})
	It("discards responses from a previously selected tab", func() {
		model := New(context.Background(), Options{})
		updated, _ := model.Update(pageMsg{generation: -1, output: "stale"})
		Expect(updated.(Model).tabs[0].output).To(BeEmpty())
	})
	It("updates the process header while events are paused and reveals exited rows on demand", func() {
		model := New(context.Background(), Options{})
		model.width, model.height = 160, 30
		model.tabs[0].paused = true
		cpu, memory, rss := 125.0, 4.0, uint64(2048)
		exited := time.Now()
		snapshot := read.ProcessSnapshot{Armed: true, Rows: []read.ProcessRow{
			{ProcessRow: processcapture.ProcessRow{Key: "7@new", PID: 7, Name: "worker", CPUPercent: &cpu, MemoryPercent: &memory, RSSBytes: &rss}},
			{ProcessRow: processcapture.ProcessRow{Key: "7@old", PID: 7, Name: "old worker", ExitedAt: &exited}},
		}}
		updated, _ := model.Update(processMsg{snapshot: snapshot})
		model = updated.(Model)
		Expect(model.tabs[0].paused).To(BeTrue())
		Expect(model.visibleProcesses()).To(HaveLen(1))
		Expect(model.View().Content).To(ContainSubstring("125.0%"))
		updated, _ = model.key("e")
		Expect(updated.(Model).visibleProcesses()).To(HaveLen(2))
	})
})
