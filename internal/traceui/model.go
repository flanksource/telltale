package traceui

import (
	"context"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/traces/read"
)

type Options struct {
	Refs   []read.Ref
	Runs   []telltale.RunInfo
	Sort   string
	Replay bool
}

type tab struct {
	name, file, cursor string
	refs               []read.Ref
	page               read.FeedResult
	history            []string
	selected           int
	paused, detail     bool
	monitor, output    string
	watermarks         []int64
	columns            []query.ColumnDef
	following          bool
	outputOffset       int64
	browse             bool
}

type pageMsg struct {
	generation      int
	page            read.FeedResult
	monitor, output string
	watermarks      []int64
	columns         []query.ColumnDef
	err             error
}
type End struct {
	Err  error
	Info *telltale.RunInfo
}
type tickMsg struct{}

type Model struct {
	ctx                                    context.Context
	options                                Options
	tabs                                   []tab
	active, run, generation, width, height int
	ended, loading                         bool
	err                                    error
	saved                                  map[int]runState
}

type runState struct {
	tabs   []tab
	active int
}

func New(ctx context.Context, options Options) Model {
	if options.Sort == "" {
		options.Sort = "seq"
	}
	m := Model{ctx: ctx, options: options, ended: options.Replay, saved: map[int]runState{}}
	m.buildTabs()
	return m
}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m Model) Init() tea.Cmd { return tea.Batch(m.load(), tick()) }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch value := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = value.Width, value.Height
	case End:
		m.ended = true
		if value.Info != nil && len(m.options.Runs) > 0 {
			m.options.Runs = slices.Clone(m.options.Runs)
			m.options.Runs[m.run] = *value.Info
		}
		if value.Err != nil {
			m.err = value.Err
			return m, tea.Quit
		}
		m.generation++
		return m, m.load()
	case tickMsg:
		if m.loading || m.tabs[m.active].paused {
			return m, tick()
		}
		m.loading = true
		return m, tea.Batch(m.load(), tick())
	case pageMsg:
		if value.generation != m.generation {
			return m, nil
		}
		m.loading = false
		if value.err != nil {
			m.err = value.err
			return m, tea.Quit
		}
		t := &m.tabs[m.active]
		if !t.paused {
			t.page, t.monitor, t.output, t.watermarks = value.page, value.monitor, value.output, value.watermarks
			t.columns = value.columns
			if t.file == "" {
				t.selected = min(t.selected, max(0, len(t.page.Rows)-1))
			}
		}
	case tea.KeyPressMsg:
		return m.key(value.String())
	}
	return m, nil
}

func (m Model) key(key string) (tea.Model, tea.Cmd) {
	t := &m.tabs[m.active]
	switch key {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "tab", "shift+tab":
		delta := 1
		if key == "shift+tab" {
			delta = -1
		}
		m.active = (m.active + delta + len(m.tabs)) % len(m.tabs)
	case "[", "]":
		if len(m.options.Runs) < 2 {
			return m, nil
		}
		delta := 1
		if key == "[" {
			delta = -1
		}
		m.saved[m.run] = runState{tabs: m.tabs, active: m.active}
		m.run = (m.run + delta + len(m.options.Runs)) % len(m.options.Runs)
		if state, ok := m.saved[m.run]; ok {
			m.tabs, m.active = state.tabs, state.active
		} else {
			m.buildTabs()
		}
	case "down", "j", "pgdown":
		t.following = false
		step := 1
		if key == "pgdown" {
			step = max(1, m.height-8)
		}
		t.selected = max(0, min(m.rowCount()-1, t.selected+step))
		return m, nil
	case "up", "k", "pgup":
		if t.following {
			t.selected = max(0, m.rowCount()-1)
			t.following = false
		}
		step := 1
		if key == "pgup" {
			step = max(1, m.height-8)
		}
		t.selected = max(0, t.selected-step)
		return m, nil
	case "home", "g":
		t.following = false
		t.selected = 0
		if t.file == "" {
			t.browse = true
			t.cursor = ""
			t.history = nil
			break
		}
		t.outputOffset = 0
	case "end", "G":
		t.following = true
		t.selected = max(0, m.rowCount()-1)
		if t.file == "" {
			t.browse = false
			t.cursor = ""
			t.history = nil
			break
		}
		t.outputOffset = -1
	case "enter":
		t.detail = !t.detail
		return m, nil
	case " ", "space":
		t.paused = !t.paused
		if t.paused {
			return m, nil
		}
	case "n":
		if t.file != "" {
			if t.outputOffset < 0 {
				t.outputOffset = min(-1, t.outputOffset+1)
			} else {
				t.outputOffset += (1 << 20)
			}
			t.selected = 0
			t.following = false
			break
		}
		if t.page.NextCursor == "" {
			return m, nil
		}
		t.history = append(t.history, t.cursor)
		t.cursor = t.page.NextCursor
	case "p":
		if t.file != "" {
			if t.outputOffset < 0 {
				t.outputOffset--
			} else {
				t.outputOffset = max(0, t.outputOffset-(1<<20))
			}
			t.selected = 0
			t.following = false
			break
		}
		if len(t.history) == 0 {
			return m, nil
		}
		t.cursor = t.history[len(t.history)-1]
		t.history = t.history[:len(t.history)-1]
	case "r":
		t.cursor = ""
		t.history = nil
		t.watermarks = nil
	case "s":
		if m.options.Sort == "seq" {
			m.options.Sort = "timestamp"
		} else {
			m.options.Sort = "seq"
		}
		for i := range m.tabs {
			m.tabs[i].cursor = ""
			m.tabs[i].history = nil
		}
	default:
		return m, nil
	}
	m.generation++
	m.loading = true
	return m, m.load()
}

func (m Model) Err() error { return m.err }

func (m *Model) buildTabs() {
	m.tabs = nil
	names := []string{}
	for _, ref := range m.options.Refs {
		if len(m.options.Runs) > 0 && ref.RunID != m.options.Runs[m.run].RunID {
			continue
		}
		owner, _ := ref.Runtime.Kinds.ResultOf(ref.Kind)
		if !slices.Contains(names, owner) {
			names = append(names, owner)
		}
	}
	slices.Sort(names)
	for _, name := range names {
		t := tab{name: name}
		for _, ref := range m.options.Refs {
			owner, _ := ref.Runtime.Kinds.ResultOf(ref.Kind)
			if owner == name && (len(m.options.Runs) == 0 || ref.RunID == m.options.Runs[m.run].RunID) {
				t.refs = append(t.refs, ref)
			}
		}
		m.tabs = append(m.tabs, t)
	}
	m.tabs = append(m.tabs, tab{name: "Stdout", file: "stdout", following: true, outputOffset: -1}, tab{name: "Stderr", file: "stderr", following: true, outputOffset: -1})
	m.active = 0
}
