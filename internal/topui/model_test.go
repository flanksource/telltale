package topui

import (
	"context"
	"errors"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/flanksource/telltale/pkg/tracing/groups"
	"github.com/flanksource/telltale/pkg/tracing/process"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakeSource serves fixed frames and processes, counting what it is asked.
type fakeSource struct {
	processes map[string][]groups.ProcessSample
	keeps     bool
	closed    bool
}

func (f *fakeSource) Next(context.Context) (groups.Frame, bool, error) {
	return groups.Frame{}, false, nil
}

func (f *fakeSource) Processes(filter groups.ProcessFilter) ([]groups.ProcessSample, bool) {
	return f.processes[filter.GroupKey], f.keeps
}

func (f *fakeSource) Close() { f.closed = true }

func percent(value float64) *float64 { return &value }

func sample(key, parent, name, kind string, cpu float64, rss uint64) groups.GroupSample {
	return groups.GroupSample{Key: key, ParentKey: parent, Name: name, Kind: kind, Total: groups.Usage{Processes: 1, CPUPercent: percent(cpu), RSSBytes: rss}}
}

// testFrame is a claude session running gavel and vitest, a brew service,
// and the system's other processes.
var testFrame = groups.Frame{
	Tick: 7, SampledAt: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC),
	Host: &groups.HostUsage{
		Cores: 8, CPUUserPercent: percent(30), CPUSystemPercent: percent(10), CPUIdlePercent: percent(60),
		MemoryUsedBytes: 8 << 30, MemoryTotalBytes: 16 << 30, SwapUsedBytes: 1 << 30, SwapTotalBytes: 4 << 30,
		DiskUsage: groups.DiskUsage{ReadOps: percent(1250), WriteOps: percent(40), ReadBps: percent(3 << 20), WriteBps: percent(512 << 10)},
	},
	Overhead: &groups.Overhead{CPUPercent: percent(1.5), TickMillis: 42},
	Groups: []groups.GroupSample{
		sample("10", "", "claude", groups.KindAgent, 40, 500<<20),
		sample("11", "10", "gavel", groups.KindTest, 30, 100<<20),
		sample("12", "10", "vitest", groups.KindTest, 5, 300<<20),
		sample("20", "", "postgresql@17", groups.KindService, 2, 200<<20),
		sample("other:system", "", "system", groups.KindOther, 9, 50<<20),
		sample("other:system:logd", "other:system", "logd", groups.KindOther, 9, 50<<20),
	},
}

func press(m Model, keys ...string) Model {
	GinkgoHelper()
	for _, key := range keys {
		var msg tea.KeyPressMsg
		switch key {
		case "up":
			msg = tea.KeyPressMsg{Code: tea.KeyUp}
		case "down":
			msg = tea.KeyPressMsg{Code: tea.KeyDown}
		case "left":
			msg = tea.KeyPressMsg{Code: tea.KeyLeft}
		case "right":
			msg = tea.KeyPressMsg{Code: tea.KeyRight}
		case "enter":
			msg = tea.KeyPressMsg{Code: tea.KeyEnter}
		case "esc":
			msg = tea.KeyPressMsg{Code: tea.KeyEscape}
		default:
			msg = tea.KeyPressMsg{Code: []rune(key)[0], Text: key}
		}
		next, _ := m.Update(msg)
		m = next.(Model)
	}
	return m
}

func shown(m Model) []string {
	var keys []string
	for _, r := range m.rows() {
		keys = append(keys, r.group.Key)
	}
	return keys
}

var _ = Describe("Model over a live Top", func() {
	It("draws the host's groups, rating CPU from the second read", func(ctx SpecContext) {
		catalog, err := groups.LoadCatalog()
		Expect(err).NotTo(HaveOccurred())
		top, err := groups.NewTop(ctx, process.NewSampler(), groups.Options{Catalog: catalog, All: true})
		Expect(err).NotTo(HaveOccurred())
		m := New(ctx, Options{Source: Live{Top: top}, Every: time.Second, Order: groups.Order{By: groups.SortCPU}, Title: "live"})
		DeferCleanup(m.Close)
		next, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
		m = next.(Model)

		for range 2 {
			next, _ = m.Update(m.read()())
			m = next.(Model)
			time.Sleep(1100 * time.Millisecond)
		}

		screen := m.render()
		Expect(screen).To(MatchRegexp(`\d+\.\d% user  \d+\.\d% kernel`))
		Expect(m.rows()).To(ContainElement(Satisfy(func(r row) bool { return r.group.Key == "other:system" && r.folded })),
			"the system's processes start folded")
		Expect(strings.Split(screen, "\n")).To(HaveLen(40))
	}, SpecTimeout(20*time.Second))
})

var _ = Describe("Model", func() {
	var source *fakeSource
	var reopened []Toggles

	newModel := func(width, height int) Model {
		GinkgoHelper()
		source = &fakeSource{keeps: true, processes: map[string][]groups.ProcessSample{
			"11": {{PID: 1101, Name: "gavel", Command: "gavel test ./...", Usage: groups.Usage{Processes: 1, CPUPercent: percent(30)}}},
			"12": {{PID: 1201, Name: "node", Command: "node vitest", Usage: groups.Usage{Processes: 1, CPUPercent: percent(5)}}},
		}}
		reopened = nil
		m := New(context.Background(), Options{
			Source: source, Every: time.Second, Order: groups.Order{By: groups.SortCPU}, Title: "live",
			Reopen: func(toggles Toggles) (Source, error) {
				reopened = append(reopened, toggles)
				return &fakeSource{}, nil
			},
		})
		next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
		next, _ = next.(Model).Update(frameMsg{frame: testFrame, ok: true})
		return next.(Model)
	}

	DescribeTable("fills the window exactly, showing origin and context only where they fit",
		func(width, height int, wide bool) {
			lines := strings.Split(newModel(width, height).render(), "\n")

			Expect(lines).To(HaveLen(height))
			for _, line := range lines {
				Expect(lipgloss.Width(line)).To(BeNumerically("<=", width), line)
			}
			Expect(strings.Contains(lines[headerLines], "ORIGIN")).To(Equal(wide))
			Expect(strings.Contains(lines[0], "overhead 1.5% CPU 42ms/tick")).To(Equal(wide), "the title ends with telltale's own cost where it fits")
			Expect(lines[1]).To(ContainSubstring("30.0% user  10.0% kernel  60.0% idle · 8 cores"))
			Expect(lines[3]).To(And(HavePrefix("SWP ["), ContainSubstring("] 1.0G / 4.0G")))
			Expect(lines[4]).To(Equal("DSK read 1.2K IOPS 3.0M/s · write 40 IOPS 512.0K/s"))
		},
		Entry("a small terminal", 80, 24, false),
		Entry("a large one", 200, 60, true),
	)

	It("shows the groups as a tree sorted by CPU, the system's other processes folded", func() {
		Expect(shown(newModel(120, 30))).To(Equal([]string{"10", "11", "12", "other:system", "20"}))
	})

	It("moves the selection, folds the selected group and selects its parent from a child", func() {
		m := newModel(120, 30)
		Expect(m.selected).To(Equal("10"))

		m = press(m, "down")
		Expect(m.selected).To(Equal("11"))
		m = press(m, "left")
		Expect(m.selected).To(Equal("10"), "folding a group with none under it selects its parent")
		m = press(m, "left")
		Expect(shown(m)).To(Equal([]string{"10", "other:system", "20"}))
		m = press(m, "right")
		Expect(shown(m)).To(Equal([]string{"10", "11", "12", "other:system", "20"}))
	})

	It("cycles the sort column and reverses it", func() {
		m := press(newModel(120, 30), "s")
		Expect(m.order).To(Equal(groups.Order{By: groups.SortMemory}))
		Expect(shown(m)).To(Equal([]string{"10", "12", "11", "20", "other:system"}))

		m = press(m, "S")
		Expect(m.order).To(Equal(groups.Order{By: groups.SortMemory, Reverse: true}))
		Expect(shown(m)[0]).To(Equal("other:system"))
	})

	It("filters to the groups that match and their ancestors, unfolding them, and clears with esc", func() {
		m := press(newModel(120, 30), "/", "l", "o", "g", "enter")
		Expect(shown(m)).To(Equal([]string{"other:system", "other:system:logd"}))
		Expect(m.selected).To(Equal("other:system"))

		m = press(m, "esc")
		Expect(m.query).To(BeEmpty())
		Expect(shown(m)).To(HaveLen(5))
	})

	It("lists the processes of the selected group and those under it, busiest first", func() {
		m := press(newModel(160, 40), "enter")
		processes, kept := m.selectedProcesses()

		Expect(kept).To(BeTrue())
		Expect(processes).To(HaveLen(2))
		Expect(processes[0].PID).To(Equal(1101))
		Expect(m.render()).To(ContainSubstring("gavel test ./..."))
	})

	It("reads from a new source with a flipped toggle, closing the old one", func() {
		m := press(newModel(120, 30), "o", "n")

		Expect(reopened).To(Equal([]Toggles{{All: true}, {All: true, Net: true}}))
		Expect(source.closed).To(BeTrue())
		Expect(m.gen).To(Equal(2))
		Expect(m.status).To(ContainSubstring("nettop"))
	})

	It("ignores a frame read for a source it has replaced", func() {
		m := press(newModel(120, 30), "o")
		next, _ := m.Update(frameMsg{gen: 0, frame: groups.Frame{Tick: 99}, ok: true})

		Expect(next.(Model).frame.Tick).To(Equal(int64(7)))
	})

	It("shows a failed read and ends the view at the second in a row", func() {
		failure := errors.New("ps: exit status 1")
		next, _ := newModel(120, 30).Update(frameMsg{err: failure})
		m := next.(Model)
		Expect(m.footer()).To(ContainSubstring("read failed: ps: exit status 1"))

		next, cmd := m.Update(frameMsg{err: failure})
		Expect(cmd()).To(Equal(tea.Quit()))
		Expect(next.(Model).Err()).To(MatchError(failure))
	})
})
