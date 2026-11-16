package traceui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/flanksource/clicky/api"
	"github.com/flanksource/telltale/pkg/traces/read"
)

type processMsg struct {
	generation int
	snapshot   read.ProcessSnapshot
	err        error
}

func (m Model) loadProcesses() tea.Cmd {
	refs := []read.Ref{}
	for _, ref := range m.options.Refs {
		if len(m.options.Runs) == 0 || ref.RunID == m.options.Runs[m.run].RunID {
			refs = append(refs, ref)
		}
	}
	return func() tea.Msg {
		snapshot, err := read.Processes(m.ctx, refs)
		return processMsg{generation: m.processGeneration, snapshot: snapshot, err: err}
	}
}

func (m Model) visibleProcesses() []read.ProcessRow {
	rows := []read.ProcessRow{}
	for _, row := range m.processes.Rows {
		if m.showExited || row.ExitedAt == nil {
			rows = append(rows, row)
		}
	}
	return rows
}

func (m Model) processLines(width, height int) []string {
	if m.processError != nil {
		return []string{fmt.Sprintf("Process header: %v", m.processError)}
	}
	if !m.processes.Armed {
		return nil
	}
	rows := m.visibleProcesses()
	label := fmt.Sprintf("Processes · %d shown · e show exited", len(rows))
	if m.showExited {
		label = fmt.Sprintf("Processes · %d shown · e hide exited (last samples)", len(rows))
	}
	lines := []string{mutedStyle.Render(label)}
	if len(rows) == 0 {
		return append(lines, "No live processes · press e to show exited")
	}
	start := min(m.processOffset, len(rows)-1)
	budget := max(1, height/3-1)
	for _, row := range rows[start:min(len(rows), start+budget)] {
		cpu, ram := "waiting", "waiting"
		if row.CPUPercent != nil {
			cpu = fmt.Sprintf("%s %.1f%%", processBar(*row.CPUPercent), *row.CPUPercent)
		}
		if row.RSSBytes != nil && row.MemoryPercent != nil {
			ram = fmt.Sprintf("%s %s %.1f%%", processBar(*row.MemoryPercent), api.HumanizeBytes(int64(*row.RSSBytes)).String(), *row.MemoryPercent)
		}
		name := row.Name
		status := ""
		if row.ExitedAt != nil {
			status = "exited"
		}
		lines = append(lines, fmt.Sprintf("%6d %-*.*s %-6s CPU %s RAM %s", row.PID, max(8, min(20, width/6)), max(8, min(20, width/6)), name, status, cpu, ram))
	}
	return lines
}

func processBar(percent float64) string {
	const width = 6
	filled := max(0, min(width, int(percent/100*width+0.5)))
	return "[" + titleStyle.Render(strings.Repeat("█", filled)) + mutedStyle.Render(strings.Repeat("░", width-filled)) + "]"
}
