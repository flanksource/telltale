package traceui

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/flanksource/clicky"
	"github.com/flanksource/clicky/tui/table"
	"github.com/flanksource/commons-db/recordstore/sampleframe"
	"github.com/flanksource/telltale/pkg/traces/read"
	"github.com/flanksource/telltale/pkg/tracing"
	tracehttp "github.com/flanksource/telltale/pkg/tracing/http"
	"github.com/flanksource/telltale/pkg/tracing/httptop"
)

type sampledTab struct {
	refs                         []read.Ref
	source, metric               string
	ref                          read.Ref
	frame                        string
	frames                       []sampleframe.Frame
	result                       *read.TopResult
	table                        table.Model
	search, filter, typing, sort string
	desc, help                   bool
	message                      string
	linked                       bool
}

func (m Model) loadTop(t tab) pageMsg {
	result := pageMsg{generation: m.generation}
	q := read.TopQuery{Frame: t.top.frame, Source: t.top.source, Metric: t.top.metric, Query: read.Query{All: true, Search: t.top.search, Sort: t.top.sort, Desc: t.top.desc}}
	if t.top.filter != "" {
		q.Filters = []string{t.top.filter}
	}
	top, err := read.Top(m.ctx, t.top.ref, q)
	if err != nil {
		result.topMessage = err.Error()
		if !errors.Is(err, read.ErrNoCompleteSamples) {
			result.topMessage = "Cannot read sample: " + err.Error()
		}
		return result
	}
	result.top = &top
	result.frames, result.err = read.Frames(m.ctx, t.top.ref)
	return result
}

func (t *sampledTab) receive(result pageMsg) error {
	t.message = result.topMessage
	if result.top == nil {
		return nil
	}
	plugin, _ := t.ref.Runtime.Kinds.Get(t.ref.Kind)
	spec := tracing.TopOf(plugin)
	ids := make([]string, len(result.top.Rows))
	for i, row := range result.top.Rows {
		values := []any{result.top.Frame.Source}
		for _, key := range spec.Identity {
			values = append(values, row[key])
		}
		if len(spec.Identity) == 0 {
			values = append(values, row["seq"])
		}
		data, err := json.Marshal(values)
		if err != nil {
			return err
		}
		ids[i] = string(data)
	}
	if err := t.table.Set(result.top.Display, ids); err != nil {
		return err
	}
	if t.ref.Kind == httptop.Kind {
		for i, row := range result.top.Display.Rows {
			direction, _ := result.top.Rows[i]["direction"].(string)
			arrow := tracehttp.DirectionArrow(tracehttp.Direction(direction))
			if cell, ok := row["direction"]; ok && arrow != "" {
				cell.Textable = clicky.Text(arrow)
				row["direction"] = cell
			}
		}
	}
	t.result, t.frames = result.top, result.frames
	if t.linked && result.top.Count == 0 {
		t.message = "Referenced blocker was omitted from this completed sample."
	}
	t.linked = false
	return nil
}

func (m Model) sampleKey(key string) (tea.Model, tea.Cmd, bool) {
	t := &m.tabs[m.active]
	s := t.top
	if s.typing != "" {
		value := &s.search
		if s.typing == "filter" {
			value = &s.filter
		}
		switch key {
		case "enter":
			s.typing = ""
		case "esc":
			s.typing = ""
			*value = ""
		case "backspace":
			runes := []rune(*value)
			if len(runes) > 0 {
				*value = string(runes[:len(runes)-1])
			}
			return m, nil, true
		default:
			if key == "space" || key == " " {
				*value += " "
			} else if len([]rune(key)) == 1 {
				*value += key
			}
			return m, nil, true
		}
	} else {
		switch key {
		case "b":
			plugin, _ := s.ref.Runtime.Kinds.Get(s.ref.Kind)
			relation := tracing.TopOf(plugin).Relation
			index := s.table.Index()
			if relation == nil || s.result == nil || index < 0 {
				return m, nil, true
			}
			id := s.result.Rows[index][relation.Column]
			if id == nil || fmt.Sprint(id) == "0" {
				s.message = "Selected row has no blocker."
				return m, nil, true
			}
			s.frame = s.result.Selection
			s.search = ""
			s.filter = relation.Target + "=" + fmt.Sprint(id)
			s.table.Detail = false
			t.paused = true
			s.linked = true
		case "c":
			if len(s.refs) > 1 {
				index := slices.IndexFunc(s.refs, func(ref read.Ref) bool { return ref.Stream == s.ref.Stream })
				s.ref = s.refs[(index+1)%len(s.refs)]
				s.frame, s.source, s.metric = "", "", ""
				s.table.Detail = false
			}
		case "o":
			sources := []string{""}
			for _, frame := range s.frames {
				if !slices.Contains(sources, frame.Source) {
					sources = append(sources, frame.Source)
				}
			}
			s.source = sources[(slices.Index(sources, s.source)+1)%len(sources)]
			s.frame = ""
			s.table.Detail = false
		case "m":
			if s.result != nil && len(s.result.Metrics) > 0 {
				metrics := s.result.Metrics
				index := slices.IndexFunc(metrics, func(metric tracing.TopMetric) bool { return metric.Key == s.metric })
				s.metric = metrics[(index+1)%len(metrics)].Key
				s.table.Detail = false
			}
		case "/":
			s.typing = "search"
			return m, nil, true
		case "f":
			s.typing = "filter"
			return m, nil, true
		case "?":
			s.help = !s.help
			return m, nil, true
		case "h":
			t.browse = true
		case "s":
			keys := []string{}
			for _, column := range s.table.Table.Columns {
				if column.SortKey != "" {
					keys = append(keys, column.SortKey)
				}
			}
			if len(keys) > 0 {
				s.sort = keys[(slices.Index(keys, s.sort)+1)%len(keys)]
				s.desc = true
			}
		case "S":
			s.desc = !s.desc
		case "n", "p":
			frames := slices.DeleteFunc(slices.Clone(s.frames), func(frame sampleframe.Frame) bool { return s.source != "" && frame.Source != s.source })
			if len(frames) == 0 {
				return m, nil, true
			}
			index := slices.IndexFunc(frames, func(f sampleframe.Frame) bool { return f.ID == s.frame })
			if index < 0 {
				index = len(frames) - 1
			}
			delta := 1
			if key == "p" {
				delta = -1
			}
			s.frame = frames[max(0, min(len(frames)-1, index+delta))].ID
			s.table.Detail = false
			t.paused = true
		case "r":
			s.frame, t.paused, s.table.Detail = "", false, false
		case "space", " ":
			if t.paused {
				s.frame = ""
				t.paused = false
				s.table.Detail = false
			} else {
				t.paused = true
				if s.result != nil {
					s.frame = s.result.Selection
				}
			}
		default:
			if !s.table.Key(key) {
				return m, nil, false
			}
			if s.table.Detail && s.result != nil {
				t.paused = true
				s.frame = s.result.Selection
				m.generation++
				m.loading = false
			}
			return m, nil, true
		}
	}
	m.generation++
	m.loading = true
	return m, m.load(), true
}

func (m Model) sampleView() tea.View {
	t := m.tabs[m.active]
	s := t.top
	width, height := max(1, m.width), max(1, m.height)
	mode := "live"
	if m.options.Replay {
		mode = "recorded"
	}
	if t.paused || s.frame != "" {
		mode = "paused"
	}
	lines := []string{fmt.Sprintf("Trace · %s · Top · %s", t.name, mode)}
	names := []string{}
	for i, item := range m.tabs {
		name := item.name
		if i == m.active {
			name = "[" + name + "]"
		}
		names = append(names, name)
	}
	lines = append(lines, strings.Join(names, " · "))
	if s.result != nil {
		f := s.result.Frame
		limit := "unlimited"
		if f.Limit > 0 {
			limit = fmt.Sprint(f.Limit)
		}
		if active, ok := f.Summary["activePlanLimit"]; ok {
			limit = fmt.Sprintf("%v active + evictions", active)
		}
		lines = append(lines, fmt.Sprintf("%s · %s · %d/%d rows · limit %s", f.Source, f.CompletedAt.Local().Format("15:04:05"), f.Retained, f.Observed, limit))
	}
	lines = append(lines, fmt.Sprintf("Search: %s · Filter: %s · Sort: %s · Metric: %s", s.search, s.filter, s.sort, s.metric))
	if s.typing != "" {
		lines = append(lines, "Editing "+s.typing+" · enter apply · esc clear")
	}
	if s.message != "" {
		lines = append(lines, s.message)
	}
	if s.help {
		lines = append(lines, "↑↓ move / scroll detail · enter inspect (freezes sample) · b blocker", "/ search · f native filter: column=expression · s sort · S reverse", "m metric · o source · c capture · p/n previous/next sample", "space pause/resume · h history · r resume live · q quit · ? close help")
	} else {
		s.table.SetSize(width, max(1, height-len(lines)-1))
		lines = append(lines, strings.Split(s.table.View(), "\n")...)
	}
	for len(lines) < height-1 {
		lines = append(lines, "")
	}
	lines = append(lines, "tab kind · / search · f filter · enter detail · h history · ? help")
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, width, "")
	}
	view := tea.NewView(strings.Join(lines[:min(len(lines), height)], "\n"))
	view.AltScreen = true
	return view
}
