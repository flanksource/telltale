package topui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/flanksource/telltale/pkg/tracing/groups"
)

// headerLines are the title, CPU, memory, swap and disk lines above the
// table.
const headerLines = 5

var (
	titleStyle    = lipgloss.NewStyle().Bold(true)
	headStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("244"))
	mutedStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	selectedStyle = lipgloss.NewStyle().Reverse(true)
	statusStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("208"))
	userStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	kernelStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	memoryStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("78"))
	swapStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	kindStyles    = map[string]lipgloss.Style{
		groups.KindAgent:   lipgloss.NewStyle().Foreground(lipgloss.Color("39")),
		groups.KindTest:    lipgloss.NewStyle().Foreground(lipgloss.Color("78")),
		groups.KindBrowser: lipgloss.NewStyle().Foreground(lipgloss.Color("170")),
		groups.KindService: lipgloss.NewStyle().Foreground(lipgloss.Color("214")),
		groups.KindOther:   mutedStyle,
	}
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
	lines := m.header()
	columns := layout(m.width)
	lines = append(lines, headStyle.Render(fit(columns.head(), m.width)))
	rows := m.rows()
	for index := m.offset; index < m.offset+m.bodyHeight(); index++ {
		if index >= len(rows) {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, m.renderRow(columns, rows[index]))
	}
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
		return padLines(titleStyle.Render(fit("telltale top · "+m.options.Title, m.width))+"\n"+mutedStyle.Render("Reading process groups…"), headerLines)
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
		return padLines(titleStyle.Render(fit(title, m.width))+"\n"+mutedStyle.Render(fit("The host's CPU, memory, swap and disks are read live, not recorded.", m.width)), headerLines)
	}
	barWidth := max(10, min(60, m.width/3))
	cpu := "CPU " + bar(barWidth, []share{{percentOf(host.CPUUserPercent), userStyle}, {percentOf(host.CPUSystemPercent), kernelStyle}}) +
		fmt.Sprintf(" %s%% user  %s%% kernel  %s%% idle · %d cores · load %.2f %.2f %.2f",
			groups.Percent(host.CPUUserPercent), groups.Percent(host.CPUSystemPercent), groups.Percent(host.CPUIdlePercent),
			host.Cores, host.Load1, host.Load5, host.Load15)
	memory := "MEM " + bar(barWidth, []share{{ratio(host.MemoryUsedBytes, host.MemoryTotalBytes), memoryStyle}}) +
		fmt.Sprintf(" %s / %s", groups.Size(float64(host.MemoryUsedBytes)), groups.Size(float64(host.MemoryTotalBytes)))
	swap := "SWP " + bar(barWidth, []share{{ratio(host.SwapUsedBytes, host.SwapTotalBytes), swapStyle}}) +
		fmt.Sprintf(" %s / %s", groups.Size(float64(host.SwapUsedBytes)), groups.Size(float64(host.SwapTotalBytes)))
	if host.SwapTotalBytes == 0 {
		swap = "SWP " + mutedStyle.Render("no swap")
	}
	disks := fmt.Sprintf("DSK read %s IOPS %s/s · write %s IOPS %s/s",
		ops(host.ReadOps), groups.Rate(host.ReadBps), ops(host.WriteOps), groups.Rate(host.WriteBps))
	return []string{titleStyle.Render(fit(title, m.width)), clip(cpu, m.width), clip(memory, m.width), clip(swap, m.width), clip(disks, m.width)}
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

// share is a part of a bar, in percent, and how it is drawn.
type share struct {
	percent float64
	style   lipgloss.Style
}

func bar(width int, shares []share) string {
	var b strings.Builder
	used := 0
	for _, part := range shares {
		cells := min(width-used, int(part.percent/100*float64(width)+0.5))
		if cells > 0 {
			b.WriteString(part.style.Render(strings.Repeat("█", cells)))
			used += cells
		}
	}
	return "[" + b.String() + mutedStyle.Render(strings.Repeat("░", width-used)) + "]"
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

// columns are the table's columns at a width: the name takes what the
// fixed ones leave, and origin and context show only where they fit.
type columns struct {
	name            int
	origin, context int
}

const fixedWidth = 6 + 7 + 8 + 9 + 9 + 9 + 9 + 6

func layout(width int) columns {
	c := columns{}
	if width >= 140 {
		c.origin = 26
	}
	if width >= 190 {
		c.context = 34
	}
	c.name = max(16, width-fixedWidth-c.origin-c.context)
	return c
}

func (c columns) head() string {
	return c.line("GROUP", "PROCS", "CPU%", "RSS", "DISK R/s", "DISK W/s", "NET IN/s", "NET OUT/s", "SOCK", "ORIGIN", "CONTEXT")
}

func (c columns) line(name, procs, cpu, rss, diskRead, diskWrite, netIn, netOut, sockets, origin, context string) string {
	text := fmt.Sprintf("%-*s%6s%7s%8s%9s%9s%9s%9s%6s", c.name, groups.Truncate(name, c.name-1), procs, cpu, rss, diskRead, diskWrite, netIn, netOut, sockets)
	if c.origin > 0 {
		text += fmt.Sprintf("  %-*s", c.origin-2, groups.Truncate(origin, c.origin-2))
	}
	if c.context > 0 {
		text += "  " + groups.Truncate(context, c.context-2)
	}
	return text
}

func (m Model) renderRow(c columns, r row) string {
	glyph := "  "
	if r.parent {
		glyph = "▾ "
		if r.folded {
			glyph = "▸ "
		}
	}
	usage := r.group.Total
	label := groups.Label(r.group)
	if m.cpuHog(usage.CPUPercent) {
		label = "! CPU hog · " + label
	}
	text := fit(c.line(strings.Repeat("  ", r.depth)+glyph+label, fmt.Sprint(usage.Processes), groups.Percent(usage.CPUPercent),
		groups.Size(float64(usage.RSSBytes)), groups.Rate(usage.DiskRead), groups.Rate(usage.DiskWrite), groups.Rate(usage.NetRx),
		groups.Rate(usage.NetTx), groups.Count(usage.Sockets), groups.OriginText(r.group.Origin), groups.ContextText(r.group.Context)), m.width)
	if r.group.Key == m.selected {
		return selectedStyle.Render(text)
	}
	if m.cpuHog(usage.CPUPercent) {
		return statusStyle.Render(text)
	}
	if style, ok := kindStyles[r.group.Kind]; ok {
		return style.Render(text)
	}
	return text
}

func (m Model) renderDetail() []string {
	height := m.detailHeight()
	if height == 0 {
		return nil
	}
	title := "processes of " + m.selected
	if m.frame != nil {
		for _, group := range m.frame.Groups {
			if group.Key == m.selected {
				title = "processes of " + groups.Label(group) + " and the groups under it"
			}
		}
	}
	lines := []string{headStyle.Render(fit("── "+title+" "+strings.Repeat("─", m.width), m.width))}
	processes, kept := m.selectedProcesses()
	if !kept {
		return append(lines, padLines(mutedStyle.Render("A recording keeps each group's use, not its processes."), height-1)...)
	}
	commandWidth := max(10, m.width-(8+12+7+8+9+9+9+9+6+24))
	lines = append(lines, headStyle.Render(fit(fmt.Sprintf("%8s %-11s%7s%8s%9s%9s%9s%9s%6s  %-22s%s",
		"PID", "USER", "CPU%", "RSS", "DISK R/s", "DISK W/s", "NET IN/s", "NET OUT/s", "SOCK", "ORIGIN", "COMMAND"), m.width)))
	start := 0
	for index, p := range processes {
		if p.Key == m.processKey {
			start = max(0, index-(height-3))
		}
	}
	for index := start; index < min(len(processes), start+height-2); index++ {
		p := processes[index]
		command := p.Command
		if m.cpuHog(p.CPUPercent) {
			command = "! CPU hog · " + command
		}
		text := fit(fmt.Sprintf("%8d %-11s%7s%8s%9s%9s%9s%9s%6s  %-22s%s",
			p.PID, groups.Truncate(p.Origin.Owner, 10), groups.Percent(p.CPUPercent), groups.Size(float64(p.RSSBytes)),
			groups.Rate(p.DiskRead), groups.Rate(p.DiskWrite), groups.Rate(p.NetRx), groups.Rate(p.NetTx), groups.Count(p.Sockets),
			groups.Truncate(groups.OriginText(p.Origin), 21), groups.Truncate(command, commandWidth)), m.width)
		if p.Key == m.processKey || (m.processKey == "" && index == 0) {
			text = selectedStyle.Render(text)
		} else if m.cpuHog(p.CPUPercent) {
			text = statusStyle.Render(text)
		}
		lines = append(lines, text)
	}
	return padLines(strings.Join(lines, "\n"), height)
}

func (m Model) footer() string {
	if m.status != "" {
		return statusStyle.Render(fit(m.status, m.width))
	}
	if m.typing {
		return mutedStyle.Render(fit("type to filter · enter keeps it · esc clears it", m.width))
	}
	if m.detail {
		return mutedStyle.Render(fit("[ ] choose process  x terminate  r nice=10  y confirm  esc cancel · ! CPU hog >25% of all cores", m.width))
	}
	toggles := fmt.Sprintf("o other:%s  n net:%s", onOff(m.toggles.All), onOff(m.toggles.Net))
	return mutedStyle.Render(fit("↑↓ move  ←→ fold  enter processes  b group  s sort  S reverse  / filter  "+toggles+"  q quit", m.width))
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

// fit pads or cuts plain text to exactly width cells.
func fit(text string, width int) string {
	if lipgloss.Width(text) > width {
		return groups.Truncate(text, width)
	}
	return text + strings.Repeat(" ", width-lipgloss.Width(text))
}

// clip cuts styled text to width cells.
func clip(text string, width int) string {
	return lipgloss.NewStyle().MaxWidth(width).Render(text)
}

// padLines is text as exactly height lines.
func padLines(text string, height int) []string {
	lines := strings.Split(text, "\n")
	for len(lines) < height {
		lines = append(lines, "")
	}
	return lines[:height]
}
