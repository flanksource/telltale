package tui

import (
	"context"
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/flanksource/clicky/api"
	"github.com/flanksource/clicky/tui/table"
	"github.com/flanksource/telltale/internal/jvm/top"
)

type Model struct {
	ctx                                 context.Context
	source                              *top.Source
	every                               time.Duration
	frame                               *top.Frame
	table                               table.Model
	inspection                          *table.Model
	width, height, sortIndex            int
	query                               string
	paused, reverse, typing, busy, help bool
	generation                          int
	err                                 error
}
type frameMsg struct {
	frame      top.Frame
	err        error
	generation int
}
type stackMsg struct {
	text       string
	err        error
	generation int
}
type tickMsg struct{ generation int }

var sorts = []string{"cpu", "name", "state", "id"}

func New(ctx context.Context, source *top.Source, every time.Duration) Model {
	return Model{ctx: ctx, source: source, every: every, busy: true}
}
func (m Model) Err() error    { return m.err }
func (m Model) Init() tea.Cmd { return m.read() }
func (m Model) read() tea.Cmd {
	return func() tea.Msg { frame, err := m.source.Next(m.ctx); return frameMsg{frame, err, m.generation} }
}
func (m *Model) refreshTable() {
	if m.frame == nil {
		return
	}
	rows := top.Order(m.frame.Threads.Threads, sorts[m.sortIndex], m.reverse, m.query)
	ids := make([]string, len(rows))
	for i, row := range rows {
		ids[i] = strconv.FormatInt(row.ID, 10)
	}
	m.err = m.table.Set(top.Table(rows), ids)
}
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case frameMsg:
		m.busy = false
		if msg.generation != m.generation || m.paused {
			if !m.paused {
				m.busy = true
				return m, m.read()
			}
			return m, nil
		}
		if msg.err != nil {
			m.err = msg.err
			return m, tea.Quit
		}
		m.frame = &msg.frame
		m.refreshTable()
		if m.err != nil {
			return m, tea.Quit
		}
		return m, tea.Tick(m.every, func(time.Time) tea.Msg { return tickMsg{m.generation} })
	case tickMsg:
		if !m.paused && !m.busy && msg.generation == m.generation {
			m.busy = true
			return m, m.read()
		}
	case stackMsg:
		if msg.generation != m.generation || m.inspection == nil {
			return m, nil
		}
		if msg.err != nil {
			m.setStack("Stack unavailable: " + msg.err.Error())
		} else {
			m.setStack(msg.text)
		}
	case tea.KeyPressMsg:
		return m.key(msg)
	}
	return m, nil
}
func (m Model) key(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	name := key.String()
	if name == "ctrl+c" {
		return m, tea.Quit
	}
	if m.inspection != nil {
		switch name {
		case "q":
			return m, tea.Quit
		case "esc", "d":
			m.inspection = nil
			return m, nil
		case "space", "P":
			m.inspection = nil
		default:
			m.inspection.Key(name)
			return m, nil
		}
	}
	if m.typing {
		switch name {
		case "enter":
			m.typing = false
		case "esc":
			m.typing = false
			m.query = ""
		case "backspace":
			runes := []rune(m.query)
			if len(runes) > 0 {
				m.query = string(runes[:len(runes)-1])
			}
		default:
			m.query += key.Text
		}
		m.refreshTable()
		return m, nil
	}
	switch name {
	case "q":
		return m, tea.Quit
	case "?":
		m.help = !m.help
	case "space", "P":
		m.paused = !m.paused
		m.generation++
		if !m.paused {
			if !m.busy {
				m.busy = true
				return m, m.read()
			}
		}
	case "enter", "d":
		if m.table.Selected == "" {
			return m, nil
		}
		id, err := strconv.ParseInt(m.table.Selected, 10, 64)
		if err != nil {
			m.err = err
			return m, tea.Quit
		}
		m.paused = true
		view := m.table
		view.Detail = true
		view.DetailOffset = 0
		m.inspection = &view
		m.generation++
		m.setStack("Reading stack…")
		return m, func() tea.Msg {
			stack, err := m.source.Stack(m.ctx, id)
			return stackMsg{stack.Text, err, m.generation}
		}
	case "esc":
		m.help = false
		m.query = ""
		m.refreshTable()
	case "/":
		m.typing = true
	case "s":
		m.sortIndex = (m.sortIndex + 1) % len(sorts)
		m.refreshTable()
	case "S":
		m.reverse = !m.reverse
		m.refreshTable()
	default:
		m.table.Key(name)
	}
	return m, nil
}
func (m *Model) setStack(text string) {
	m.inspection.Table.RowDetail = make([]api.Textable, len(m.inspection.IDs))
	m.inspection.Table.RowDetail[m.inspection.Index()] = api.Text{}.Append(text)
}
