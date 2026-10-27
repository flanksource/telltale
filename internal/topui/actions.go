package topui

import (
	"context"
	"fmt"
	"slices"

	tea "charm.land/bubbletea/v2"
	"github.com/flanksource/telltale/internal/processcontrol"
	"github.com/flanksource/telltale/pkg/tracing/groups"
)

type controller interface {
	Control(context.Context, processcontrol.Request) error
}
type actionMsg struct{ text string }

func (l Live) Control(ctx context.Context, request processcontrol.Request) error {
	return processcontrol.Apply(ctx, request)
}

func (m Model) cpuHog(cpu *float64) bool {
	return cpu != nil && m.frame != nil && m.frame.Host != nil && m.frame.Host.Cores > 0 && *cpu > 25*float64(m.frame.Host.Cores)
}

func (m *Model) cycleProcess(key string) {
	processes, _ := m.selectedProcesses()
	if len(processes) == 0 {
		return
	}
	index := slices.IndexFunc(processes, func(p groups.ProcessSample) bool { return p.Key == m.processKey })
	step := 1
	if key == "[" {
		step = -1
	}
	m.processKey = processes[(max(index, 0)+step+len(processes))%len(processes)].Key
	m.detail = true
	m.status = ""
}

func (m Model) prepareAction(key string) (tea.Model, tea.Cmd) {
	if m.acting {
		return m, nil
	}
	if _, ok := m.source.(controller); !ok {
		m.status = "Process control is available only for live processes"
		return m, nil
	}
	processes, _ := m.selectedProcesses()
	if len(processes) == 0 {
		m.status = "No live process selected"
		return m, nil
	}
	index := slices.IndexFunc(processes, func(p groups.ProcessSample) bool { return p.Key == m.processKey })
	if index < 0 && m.processKey != "" {
		m.status = "Selected process is no longer listed; use [ or ] to choose another"
		return m, nil
	}
	p := processes[max(index, 0)]
	request := processcontrol.Request{Key: p.Key, Action: "terminate"}
	label := "Send SIGTERM to"
	if key == "r" {
		request.Action, request.Nice = "renice", new(10)
		label = "Set nice to 10 for"
	}
	m.pending, m.detail = &request, true
	m.status = fmt.Sprintf("%s %s PID %d? y confirm / n cancel", label, p.Name, p.PID)
	return m, nil
}

func (m Model) confirmAction(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.Text == "n" || msg.Code == tea.KeyEscape {
		m.pending, m.status = nil, ""
		return m, nil
	}
	if msg.Text != "y" {
		return m, nil
	}
	request, control, ctx := *m.pending, m.source.(controller), m.ctx
	m.pending, m.acting, m.status = nil, true, "Applying process action…"
	return m, func() tea.Msg {
		if err := control.Control(ctx, request); err != nil {
			return actionMsg{text: err.Error()}
		}
		return actionMsg{text: fmt.Sprintf("%s requested for %s", request.Action, request.Key)}
	}
}
