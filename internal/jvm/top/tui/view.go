package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/flanksource/clicky/api"
	"github.com/flanksource/telltale/internal/terminalui"
	"github.com/flanksource/telltale/pkg/tracing/jvm/jvmruntime"
)

func (m Model) View() tea.View {
	if m.width <= 0 || m.height <= 0 {
		return tea.NewView("Reading JVM…")
	}
	header := m.header()
	footer := m.footer()
	bodyHeight := max(0, m.height-min(len(header), max(1, m.height-3))-1)
	header = header[:min(len(header), max(1, m.height-3))]
	body := m.table.View(m.width, bodyHeight)
	if m.inspection != nil {
		header = []string{terminalui.Title.Render("JVM thread stack · paused")}
		bodyHeight = max(0, m.height-2)
		body = m.inspection.View(m.width, bodyHeight)
	}
	if m.help {
		header = []string{terminalui.Title.Render("JVM Top help")}
		bodyHeight = max(0, m.height-2)
		body = "↑↓ or j/k select · s sort · S reverse\n/ filter · enter keeps filter · esc clears filter\nd or enter inspect stack and freeze\nP or space pause/resume\nIn stack: ↑↓ scroll · esc or d close · P resume\n? close help · q or Ctrl+C quit"
	}
	lines := append(header, terminalui.PadLines(body, bodyHeight)...)
	lines = append(lines, footer)
	lines = lines[:min(len(lines), m.height)]
	for i := range lines {
		lines[i] = terminalui.Clip(lines[i], m.width)
	}
	view := tea.NewView(strings.Join(lines, "\n"))
	view.AltScreen = true
	return view
}

func (m Model) header() []string {
	direction := "↓"
	if m.reverse {
		direction = "↑"
	}
	title := "telltale jvm top · sort " + sorts[m.sortIndex] + direction
	if m.paused {
		title += " · paused"
	}
	if m.typing || m.query != "" {
		title += " · filter " + m.query
		if m.typing {
			title += "▏"
		}
	}
	if m.frame == nil {
		return terminalui.PadLines(terminalui.Title.Render(terminalui.Fit(title, m.width))+"\n"+terminalui.Muted.Render("Reading JVM…"), 6)
	}
	f := m.frame
	title += fmt.Sprintf(" · %s · %s · Java %s · up %s", f.SampledAt.Local().Format(time.TimeOnly), f.AgentID, f.Metrics.Runtime.JavaVersion, (time.Duration(f.Metrics.Runtime.Uptime) * time.Millisecond).Round(time.Second))
	width := max(1, min(60, m.width/3))
	cpu := "CPU " + terminalui.Muted.Render("unavailable")
	if value := f.Metrics.ProcessCPUPercent; value != nil {
		cpu = "CPU " + terminalui.Bar(width, []terminalui.Share{{Percent: *value, Style: terminalui.CPU}}) + fmt.Sprintf(" %.1f%% of capacity", *value)
	}
	used, committed := memory(f.Metrics.Memory.Heap)
	heap := "HEAP " + terminalui.Muted.Render("unavailable")
	if committed > 0 {
		heap = "HEAP " + terminalui.Bar(width, []terminalui.Share{{Percent: 100 * float64(used) / float64(committed), Style: terminalui.Memory}}) + fmt.Sprintf(" %s / %s committed", api.HumanizeBytes(used), api.HumanizeBytes(committed))
	}
	nonHeap, _ := memory(f.Metrics.Memory.NonHeap)
	buffers, _ := memory(f.Metrics.Memory.Buffer)
	gc := []string{}
	for _, collector := range f.Metrics.GarbageCollectors {
		gc = append(gc, fmt.Sprintf("%s %s / %sms", collector.Name, counter(collector.CollectionCount), counter(collector.CollectionTime)))
	}
	states := map[string]int{}
	for _, thread := range f.Threads.Threads {
		states[thread.State]++
	}
	threads := fmt.Sprintf("THR %d · runnable %d · blocked %d · waiting %d", len(f.Threads.Threads), states["RUNNABLE"], states["BLOCKED"], states["WAITING"]+states["TIMED_WAITING"])
	if !f.Threads.CPUTimeEnabled {
		threads += " · CPU timing off"
	}
	return []string{terminalui.Title.Render(terminalui.Fit(title, m.width)), cpu, heap, fmt.Sprintf("MEM non-heap %s · buffers %s", api.HumanizeBytes(nonHeap), api.HumanizeBytes(buffers)), "GC " + strings.Join(gc, " · ") + " (lifetime count / time)", threads}
}

func (m Model) footer() string {
	text := "P pause/resume · d stack · / filter · ? help · q quit"
	if m.typing {
		text = "type to filter · enter keeps it · esc clears it"
	}
	if m.inspection != nil {
		text = "↑↓ scroll · esc close · P or space resume live · q quit"
	}
	if m.err != nil {
		return terminalui.Status.Render(terminalui.Fit(m.err.Error(), m.width))
	}
	if m.frame != nil && len(m.frame.Threads.Deadlocked) > 0 {
		return terminalui.Status.Render(terminalui.Fit(fmt.Sprintf("DEADLOCK threads %v · d inspect · ? help", m.frame.Threads.Deadlocked), m.width))
	}
	return terminalui.Muted.Render(terminalui.Fit(text, m.width))
}

func memory(pools []jvmruntime.MemoryPool) (used, committed int64) {
	for _, pool := range pools {
		used += pool.Used
		committed += pool.Committed
	}
	return
}
func counter(value int64) string {
	if value < 0 {
		return "unavailable"
	}
	return fmt.Sprint(value)
}
