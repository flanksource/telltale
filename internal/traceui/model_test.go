package traceui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"github.com/flanksource/telltale/pkg/traces/read"
	"github.com/flanksource/telltale/pkg/tracing"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"testing"
)

func TestTraceUI(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Trace UI") }

var _ = Describe("record model", func() {
	runtime := &tracing.Runtime{Kinds: tracing.NewKinds()}
	It("keeps enabled empty kinds visible and uses an alternate screen", func() {
		model := New(context.Background(), Options{Refs: []read.Ref{{Runtime: runtime, TraceRef: tracing.TraceRef{Kind: "sql", Stream: "s"}}, {Runtime: runtime, TraceRef: tracing.TraceRef{Kind: "http", Stream: "h"}}}})
		Expect(model.tabs).To(HaveLen(4))
		Expect(model.tabs[0].name).To(Equal("http"))
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
})
