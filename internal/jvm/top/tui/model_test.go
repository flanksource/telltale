package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"github.com/charmbracelet/x/ansi"
	"github.com/flanksource/telltale/internal/jvm/top"
	"github.com/flanksource/telltale/pkg/tracing/jvm/jvmruntime"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"strings"
	"testing"
	"time"
)

func TestTopTUI(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "JVM Top TUI") }

var _ = Describe("JVM Top lifecycle", func() {
	It("uses the shared wrapped detail viewport and remains paused when inspection closes", func() {
		m := New(context.Background(), &top.Source{}, time.Second)
		m.width, m.height = 40, 10
		next, _ := m.Update(frameMsg{frame: top.Frame{Threads: jvmruntime.ThreadSample{Threads: []jvmruntime.Thread{{ID: 1, Name: "worker"}}}}})
		m = next.(Model)
		next, _ = m.key(tea.KeyPressMsg{Text: "d"})
		m = next.(Model)
		next, _ = m.Update(stackMsg{text: strings.Repeat("wide stack frame with source filename\n", 30) + "last frame", generation: m.generation})
		m = next.(Model)
		for range 100 {
			next, _ = m.key(tea.KeyPressMsg{Code: tea.KeyDown})
			m = next.(Model)
		}
		Expect(ansi.Strip(m.View().Content)).To(ContainSubstring("last frame"))
		next, _ = m.key(tea.KeyPressMsg{Code: tea.KeyEscape})
		m = next.(Model)
		Expect(m.paused).To(BeTrue())
		Expect(m.inspection).To(BeNil())
		Expect(m.table.Selected).To(Equal("1"))
	})
	It("uses a compact meter header and keeps its footer at the bottom after resizing", func() {
		m := New(context.Background(), &top.Source{}, time.Second)
		frame := top.Frame{AgentID: "fixture", Metrics: jvmruntime.Metrics{ProcessCPUPercent: new(float64(25)), Memory: jvmruntime.Memory{Heap: []jvmruntime.MemoryPool{{Used: 128, Committed: 256}}}}, Threads: jvmruntime.ThreadSample{Threads: []jvmruntime.Thread{{ID: 1, Name: "worker"}}}}
		next, _ := m.Update(frameMsg{frame: frame})
		m = next.(Model)
		for _, size := range []tea.WindowSizeMsg{{Width: 120, Height: 30}, {Width: 80, Height: 24}, {Width: 40, Height: 10}} {
			next, _ = m.Update(size)
			m = next.(Model)
			lines := strings.Split(ansi.Strip(m.View().Content), "\n")
			Expect(lines).To(HaveLen(size.Height))
			Expect(lines[0]).To(ContainSubstring("telltale jvm top"))
			Expect(lines[1]).To(ContainSubstring("["))
			Expect(lines[len(lines)-1]).To(ContainSubstring("P pause"))
			for _, line := range lines {
				Expect(ansi.StringWidth(line)).To(BeNumerically("<=", size.Width))
			}
		}
	})
	It("supports host Top's pause and help keys and Escape clears the filter", func() {
		m := New(context.Background(), &top.Source{}, time.Second)
		next, _ := m.key(tea.KeyPressMsg{Text: "P"})
		m = next.(Model)
		Expect(m.paused).To(BeTrue())
		m.typing = true
		m.query = "worker"
		next, _ = m.key(tea.KeyPressMsg{Code: tea.KeyEscape})
		m = next.(Model)
		Expect(m.query).To(BeEmpty())
		next, _ = m.key(tea.KeyPressMsg{Text: "?"})
		m = next.(Model)
		m.width, m.height = 120, 24
		Expect(ansi.Strip(m.View().Content)).To(ContainSubstring("JVM Top help"))
	})
	It("preserves selection on reorder and ignores frames while paused", func() {
		m := New(context.Background(), &top.Source{}, time.Second)
		frame := top.Frame{Threads: jvmruntime.ThreadSample{Threads: []jvmruntime.Thread{{ID: 1, Name: "worker"}, {ID: 2, Name: "main"}}}}
		next, _ := m.Update(frameMsg{frame: frame})
		m = next.(Model)
		m.table.Selected = "2"
		m.refreshTable()
		Expect(m.table.Selected).To(Equal("2"))
		m.paused = true
		next, _ = m.Update(frameMsg{frame: top.Frame{AgentID: "other"}})
		Expect(next.(Model).frame.AgentID).To(BeEmpty())
	})
	It("never schedules an overlapping sample when resumed before a pending sample finishes", func() {
		m := New(context.Background(), &top.Source{}, time.Second)
		next, _ := m.key(tea.KeyPressMsg{Code: tea.KeySpace})
		m = next.(Model)
		next, cmd := m.key(tea.KeyPressMsg{Code: tea.KeySpace})
		Expect(cmd).To(BeNil())
		m = next.(Model)
		_, cmd = m.Update(frameMsg{generation: 0})
		Expect(cmd).NotTo(BeNil())
	})
	It("clips output on small terminals and reports sampling failures", func() {
		m := New(context.Background(), &top.Source{}, time.Second)
		next, _ := m.Update(tea.WindowSizeMsg{Width: 20, Height: 5})
		Expect(next.(Model).View().Content).NotTo(BeEmpty())
		next, cmd := m.Update(frameMsg{err: context.DeadlineExceeded})
		Expect(next.(Model).Err()).To(MatchError(context.DeadlineExceeded))
		Expect(cmd).NotTo(BeNil())
	})
})
