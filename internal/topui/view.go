package topui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/flanksource/telltale/internal/terminalui"
	"github.com/flanksource/telltale/pkg/tracing/groups"
)

// headerLines are the title, CPU, memory, swap, disk and gavel slots lines
// above the table.
const headerLines = 6

var (
	titleStyle    = terminalui.Title
	headStyle     = terminalui.Heading
	mutedStyle    = terminalui.Muted
	selectedStyle = terminalui.Selected
	statusStyle   = terminalui.Status
	userStyle     = terminalui.CPU
	kernelStyle   = terminalui.Kernel
	memoryStyle   = terminalui.Memory
	swapStyle     = terminalui.Swap
)

func (m Model) View() tea.View {
	view := tea.NewView(m.render())
	view.AltScreen = true
	return view
}

func (m Model) render() string {
	if m.width <= 0 || m.height <= 0 {
		return "Reading process groups…"
	}
	if m.inspection != nil {
		return "Recorded process detail · paused\n" + m.inspection.View(m.width, max(1, m.height-2)) + "\n↑↓ scroll · esc close · space resume live"
	}
	if m.help {
		return "Process Top\n↑↓ select · ←→ fold · enter list processes\ns sort · S reverse · / filter · b group\nd inspect and freeze · P pause/resume\nf process pane · x terminate · r nice (confirmation required)\n? close help · q quit"
	}
	lines := m.header()
	lines = append(lines, terminalui.PadLines(m.processTable().View(m.width, max(1, m.bodyHeight()+1)), max(1, m.bodyHeight()+1))...)
	lines = append(lines, m.renderDetail()...)
	lines = append(lines, m.footer())
	return strings.Join(lines[:min(len(lines), m.height)], "\n")
}

func (m Model) detailHeight() int {
	if !m.detail || m.height < 16 {
		return 0
	}
	return max(6, m.height*2/5)
}

func (m Model) bodyHeight() int {
	return m.height - headerLines - 1 - 1 - m.detailHeight()
}

func (m Model) header() []string {
	if m.frame == nil {
		return terminalui.PadLines(titleStyle.Render(terminalui.Fit("telltale top · "+m.options.Title, m.width))+"\n"+mutedStyle.Render("Reading process groups…"), headerLines)
	}
	processes := 0
	present := map[string]bool{}
	for _, group := range m.frame.Groups {
		present[group.Key] = true
	}
	for _, group := range m.frame.Groups {
		if !present[group.ParentKey] {
			processes += group.Total.Processes
		}
	}
	direction := "↓"
	if m.order.Reverse {
		direction = "↑"
	}
	title := fmt.Sprintf("telltale top · %s · %s · tick %d · %d groups · %d processes · by %s · sort %s%s",
		m.options.Title, m.frame.SampledAt.Local().Format("15:04:05"), m.frame.Tick, len(m.frame.Groups), processes, m.groupBy, m.order.By, direction)
	if m.paused {
		title += " · paused"
	}
	if overhead := m.frame.Overhead; overhead != nil {
		title += fmt.Sprintf(" · overhead %s%% CPU %.0fms/tick", groups.Percent(overhead.CPUPercent), overhead.TickMillis)
	}
	if m.query != "" || m.typing {
		title += " · filter " + m.query
		if m.typing {
			title += "▏"
		}
	}
	host := m.frame.Host
	if host == nil {
		return terminalui.PadLines(titleStyle.Render(terminalui.Fit(title, m.width))+"\n"+mutedStyle.Render(terminalui.Fit("The host's CPU, memory, swap and disks are read live, not recorded.", m.width)), headerLines)
	}
	barWidth := max(10, min(60, m.width/3))
	cpu := "CPU " + terminalui.Bar(barWidth, []terminalui.Share{{Percent: percentOf(host.CPUUserPercent), Style: userStyle}, {Percent: percentOf(host.CPUSystemPercent), Style: kernelStyle}}) +
		fmt.Sprintf(" %s%% user  %s%% kernel  %s%% idle · %d cores · load %.2f %.2f %.2f",
			groups.Percent(host.CPUUserPercent), groups.Percent(host.CPUSystemPercent), groups.Percent(host.CPUIdlePercent),
			host.Cores, host.Load1, host.Load5, host.Load15)
	memory := "MEM " + terminalui.Bar(barWidth, []terminalui.Share{{Percent: ratio(host.MemoryUsedBytes, host.MemoryTotalBytes), Style: memoryStyle}}) +
		fmt.Sprintf(" %s / %s", groups.Size(float64(host.MemoryUsedBytes)), groups.Size(float64(host.MemoryTotalBytes)))
	swap := "SWP " + terminalui.Bar(barWidth, []terminalui.Share{{Percent: ratio(host.SwapUsedBytes, host.SwapTotalBytes), Style: swapStyle}}) +
		fmt.Sprintf(" %s / %s", groups.Size(float64(host.SwapUsedBytes)), groups.Size(float64(host.SwapTotalBytes)))
	if host.SwapTotalBytes == 0 {
		swap = "SWP " + mutedStyle.Render("no swap")
	}
	disks := fmt.Sprintf("DSK read %s IOPS %s/s · write %s IOPS %s/s",
		ops(host.ReadOps), groups.Rate(host.ReadBps), ops(host.WriteOps), groups.Rate(host.WriteBps))
	slots := "SLT " + mutedStyle.Render("no gavel slot held or awaited")
	if len(m.frame.Slots) > 0 {
		slots = "SLT gavel " + groups.SlotsText(m.frame.Slots)
	}
	return []string{titleStyle.Render(terminalui.Fit(title, m.width)), terminalui.Clip(cpu, m.width), terminalui.Clip(memory, m.width), terminalui.Clip(swap, m.width), terminalui.Clip(disks, m.width), terminalui.Clip(slots, m.width)}
}

// ops is a known operations rate, in thousands from a thousand, or "-".
func ops(value *float64) string {
	switch {
	case value == nil:
		return "-"
	case *value >= 1000:
		return fmt.Sprintf("%.1fK", *value/1000)
	}
	return fmt.Sprintf("%.0f", *value)
}

func percentOf(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

func ratio(part, whole uint64) float64 {
	if whole == 0 {
		return 0
	}
	return 100 * float64(part) / float64(whole)
}

func (m Model) renderDetail() []string {
	height := m.detailHeight()
	if height == 0 {
		return nil
	}
	title := m.selected
	if rows := m.rows(); m.selectedIndex(rows) >= 0 {
		selected := rows[m.selectedIndex(rows)]
		title = groups.Label(selected.group)
		if selected.process != nil {
			title = fmt.Sprintf("%d %s", selected.process.PID, groups.ShortCommand(selected.process.Command))
		}
	}
	processes, kept := m.selectedProcesses()
	if kept {
		title += fmt.Sprintf(" · %d processes", len(processes))
	}
	heading := "── " + groups.Truncate(title, max(1, m.width-4)) + " "
	lines := []string{headStyle.Render(heading + strings.Repeat("─", max(0, m.width-lipgloss.Width(heading))))}
	rows := height
	if !kept {
		return append(lines, terminalui.PadLines(mutedStyle.Render("A recording keeps each group's use, not its processes."), rows-1)...)
	}
	commandWidth := max(10, m.width-(8+12+7+8+8+8+9+9+9+9+6+24))
	lines = append(lines, headStyle.Render(terminalui.Fit(fmt.Sprintf("%8s %-11s%7s%8s%8s%8s%9s%9s%9s%9s%6s  %-22s%s", "PID", "USER", "CPU%", "RSS", "CMPRS", "SWAP", "DISK R/s", "DISK W/s", "NET IN/s", "NET OUT/s", "SOCK", "ORIGIN", "COMMAND"), m.width)))
	start := 0
	for index, p := range processes {
		if p.Key == m.processKey {
			start = max(0, index-(rows-3))
		}
	}
	for index := start; index < min(len(processes), start+rows-2); index++ {
		p := processes[index]
		command := p.Command
		if hog := m.hogLabel(p.Usage); hog != "" {
			command = "! " + hog + " · " + command
		}
		text := terminalui.Fit(fmt.Sprintf("%8d %-11s%7s%8s%8s%8s%9s%9s%9s%9s%6s  %-22s%s", p.PID, groups.Truncate(p.Origin.Owner, 10), groups.Percent(p.CPUPercent), groups.Size(float64(p.RSSBytes)), groups.MemorySize(p.CompressedBytes), groups.MemorySize(p.SwapBytes), groups.Rate(p.DiskRead), groups.Rate(p.DiskWrite), groups.Rate(p.NetRx), groups.Rate(p.NetTx), groups.Count(p.Sockets), groups.Truncate(groups.OriginText(p.Origin), 21), groups.Truncate(command, commandWidth)), m.width)

		if p.Key == m.processKey || (m.processKey == "" && index == 0) {
			text = selectedStyle.Render(text)
		} else if m.hogLabel(p.Usage) != "" {
			text = statusStyle.Render(text)
		}
		lines = append(lines, text)
	}
	return terminalui.PadLines(strings.Join(lines, "\n"), height)
}

func (m Model) footer() string {
	if m.status != "" {
		return statusStyle.Render(terminalui.Fit(m.status, m.width))
	}
	if m.typing {
		return mutedStyle.Render(terminalui.Fit("type to filter · enter keeps it · esc clears it", m.width))
	}
	if m.detail {
		return mutedStyle.Render(terminalui.Fit("[ ] choose process  x terminate  r nice=10  y confirm  esc cancel  f close · hogs pinned 120s · CMPRS includes swapped-out pages on macOS", m.width))
	}
	toggles := fmt.Sprintf("o other:%s  n net:%s", onOff(m.toggles.All), onOff(m.toggles.Net))
	return mutedStyle.Render(terminalui.Fit("↑↓ move  enter processes  d inspect  P pause/resume  / filter  ? help  "+toggles+"  q quit", m.width))
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}
