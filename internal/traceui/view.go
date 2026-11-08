package traceui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/flanksource/clicky"
	"github.com/flanksource/telltale/internal/terminalui"
)

var titleStyle = terminalui.Title
var mutedStyle = terminalui.Muted
var selectedStyle = terminalui.Selected

func (m Model) rowCount() int {
	t := m.tabs[m.active]
	if t.file != "" {
		return len(strings.Split(t.output, "\n"))
	}
	return len(t.page.Rows)
}

func (m Model) View() tea.View {
	width, height := max(1, m.width), max(1, m.height)
	t := m.tabs[m.active]
	status := "Following"
	if m.ended {
		status = "Capture complete"
	}
	if t.paused {
		status = "Paused"
	}
	title := "Trace · " + status
	if len(m.options.Runs) > 0 {
		r := m.options.Runs[m.run]
		elapsed := time.Since(r.StartedAt)
		if !r.EndedAt.IsZero() {
			elapsed = r.EndedAt.Sub(r.StartedAt)
		}
		title = fmt.Sprintf("%s · %s · %s · %s", title, r.RunID, elapsed.Round(time.Second), strings.Join(r.Command, " "))
	}
	names := []string{}
	for i, tab := range m.tabs {
		name := " " + tab.name + " "
		if i == m.active {
			name = selectedStyle.Render(name)
		}
		names = append(names, name)
	}
	tabLine := strings.Join(names, " ")
	if ansi.StringWidth(tabLine) > width {
		tabLine = "‹ " + strings.Join(names[m.active:], " ") + " ›"
	}
	lines := []string{titleStyle.Render(title), tabLine}
	lines = append(lines, m.processLines(width, height)...)
	monitor := strings.TrimSpace(t.monitor)
	if monitor == "" {
		monitor = "Waiting for " + t.name + "…"
	}
	if t.file != "" {
		monitor = t.name + " · " + status
	}
	monitorLines := strings.Split(ansi.Wrap(monitor, width, ""), "\n")
	lines = append(lines, monitorLines[:min(len(monitorLines), max(1, height/4))]...)
	body := []string{}
	if t.file != "" {
		body = strings.Split(ansi.Strip(t.output), "\n")
	} else {
		for i, row := range t.page.Rows {
			text, err := clicky.Format(row.Pretty(), clicky.FormatOptions{Format: "pretty"})
			if err != nil {
				text = err.Error()
			}
			text = strings.ReplaceAll(text, "\n", " ")
			if i == t.selected {
				text = selectedStyle.Render(text)
			}
			body = append(body, text)
		}
	}
	available := max(0, height-len(lines)-1)
	detail := []string{}
	if t.detail && t.file == "" && len(t.page.Rows) > 0 {
		text, err := clicky.Format(t.page.Rows[t.selected].PrettyFull(), clicky.FormatOptions{Format: "pretty"})
		if err != nil {
			text = err.Error()
		}
		detail = strings.Split(text, "\n")
		detail = detail[:min(len(detail), available/2)]
		available -= len(detail)
	}
	start := max(0, t.selected-available+1)
	if t.file != "" && t.following {
		start = max(0, len(body)-available)
	}
	for i := start; i < min(len(body), start+available); i++ {
		lines = append(lines, body[i])
	}
	for len(lines) < height-1-len(detail) {
		lines = append(lines, "")
	}
	lines = append(lines, detail...)
	footer := "q quit · tab kind · ↑↓ events · e exited · alt+↑↓ processes · space pause · n/p page · enter detail · s sort · [/] run"
	lines = append(lines, mutedStyle.Render(footer))
	if len(lines) > height {
		lines = append(lines[:height-1], mutedStyle.Render(footer))
	}
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, width, "")
	}
	view := tea.NewView(strings.Join(lines, "\n"))
	view.AltScreen = true
	return view
}
