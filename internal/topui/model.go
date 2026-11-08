package topui

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/flanksource/telltale/internal/processcontrol"
	"github.com/flanksource/telltale/pkg/tracing/groups"
)

// Toggles are the reading choices the o and n keys flip.
type Toggles struct {
	// All reads the processes in no tool's group too.
	All bool
	// Net reads network bytes with nettop.
	Net bool
}

// Options make a Model.
type Options struct {
	Source Source
	// Reopen makes a source reading with other toggles; nil fixes them, as a
	// recording's are.
	Reopen  func(Toggles) (Source, error)
	Toggles Toggles
	Every   time.Duration
	Order   groups.Order
	GroupBy groups.GroupBy
	// Title says what is shown, such as where a recording goes.
	Title string
	// Done ends the view once closed, as a recording's run ends.
	Done <-chan struct{}
}

// Model is the view's state: the latest frame, the selected group and how
// the tree is folded, filtered and sorted.
type Model struct {
	ctx      context.Context
	options  Options
	source   Source
	toggles  Toggles
	order    groups.Order
	gen      int
	frame    *groups.Frame
	raw      *groups.Frame
	groupBy  groups.GroupBy
	width    int
	height   int
	selected string
	offset   int
	folded   map[string]bool
	// expanded are the groups whose member processes the tree lists.
	expanded   map[string]bool
	query      string
	typing     bool
	detail     bool
	status     string
	err        error
	failures   int
	processKey string
	pending    *processcontrol.Request
	acting     bool
}

// New is a Model reading options.Source until ctx ends.
func New(ctx context.Context, options Options) Model {
	if options.GroupBy == "" {
		options.GroupBy = groups.GroupTree
	}
	return Model{
		ctx: ctx, options: options, source: options.Source, toggles: options.Toggles, order: options.Order,
		groupBy: options.GroupBy,
		folded:  map[string]bool{}, expanded: map[string]bool{},
	}
}

// Err is why the view ended, when a read failed twice in a row.
func (m Model) Err() error { return m.err }

// Close closes the source the view reads last.
func (m Model) Close() { m.source.Close() }

type frameMsg struct {
	gen   int
	frame groups.Frame
	ok    bool
	err   error
}

type tickMsg struct{ gen int }

type doneMsg struct{}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.read()}
	if m.options.Done != nil {
		done := m.options.Done
		cmds = append(cmds, func() tea.Msg { <-done; return doneMsg{} })
	}
	return tea.Batch(cmds...)
}

func (m Model) read() tea.Cmd {
	source, gen, ctx := m.source, m.gen, m.ctx
	return func() tea.Msg {
		frame, ok, err := source.Next(ctx)
		return frameMsg{gen: gen, frame: frame, ok: ok, err: err}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case frameMsg:
		return m.received(msg)
	case tickMsg:
		if msg.gen == m.gen {
			return m, m.read()
		}
	case doneMsg:
		return m, tea.Quit
	case actionMsg:
		m.acting = false
		m.status = msg.text
		return m, nil
	case tea.KeyPressMsg:
		return m.pressed(msg)
	}
	m.scroll()
	return m, nil
}

// received takes a frame, or a failed read: the first failure is shown,
// the second in a row ends the view.
func (m Model) received(msg frameMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.gen {
		return m, nil
	}
	next := tea.Tick(m.options.Every, func(time.Time) tea.Msg { return tickMsg{gen: msg.gen} })
	switch {
	case msg.err != nil && (errors.Is(msg.err, context.Canceled) || m.ctx.Err() != nil):
		return m, tea.Quit
	case msg.err != nil:
		m.failures++
		m.err = msg.err
		if m.failures > 1 {
			return m, tea.Quit
		}
		m.status = "read failed: " + msg.err.Error()
		return m, next
	}
	m.failures, m.err = 0, nil
	if m.status != "" && strings.HasPrefix(m.status, "read failed") {
		m.status = ""
	}
	if msg.ok {
		m.raw = &msg.frame
		m.resort()
		if m.err != nil {
			return m, tea.Quit
		}
	}
	m.scroll()
	return m, next
}

func (m Model) pressed(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.Keystroke() == "ctrl+c" {
		return m, tea.Quit
	}
	if m.typing {
		return m.typed(msg), nil
	}
	if m.pending != nil {
		return m.confirmAction(msg)
	}
	rows := m.rows()
	index := m.selectedIndex(rows)
	switch msg.Code {
	case tea.KeyUp:
		m.selectAt(rows, index-1)
	case tea.KeyDown:
		m.selectAt(rows, index+1)
	case tea.KeyPgUp:
		m.selectAt(rows, index-m.bodyHeight())
	case tea.KeyPgDown:
		m.selectAt(rows, index+m.bodyHeight())
	case tea.KeyHome:
		m.selectAt(rows, 0)
	case tea.KeyEnd:
		m.selectAt(rows, len(rows)-1)
	case tea.KeyLeft:
		m.fold(rows, index, true)
	case tea.KeyRight:
		m.fold(rows, index, false)
	case tea.KeySpace:
		if index >= 0 && rows[index].parent {
			m.fold(rows, index, !rows[index].folded)
		}
	case tea.KeyEnter:
		if index >= 0 {
			m.toggleExpanded(rows[index])
		}
	case tea.KeyEscape:
		m.detail, m.query = false, ""
	default:
		return m.command(msg.Text)
	}
	m.scroll()
	return m, nil
}

// command runs a key that names one: sort, filter, toggles, quit.
func (m Model) command(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "q":
		return m, tea.Quit
	case "f":
		m.detail = !m.detail
	case "[", "]":
		m.cycleProcess(key)
	case "x", "r":
		return m.prepareAction(key)
	case "j", "k", "g", "G":
		rows := m.rows()
		index := m.selectedIndex(rows)
		m.selectAt(rows, map[string]int{"j": index + 1, "k": index - 1, "g": 0, "G": len(rows) - 1}[key])
	case "s":
		m.order = groups.Order{By: groups.Sorts[(slices.Index(groups.Sorts, m.order.By)+1)%len(groups.Sorts)]}
		m.resort()
	case "S":
		m.order.Reverse = !m.order.Reverse
		m.resort()
	case "b":
		m.groupBy = groups.Groupings[(slices.Index(groups.Groupings, m.groupBy)+1)%len(groups.Groupings)]
		m.resort()
	case "/":
		m.typing = true
	case "o":
		return m.reopen(Toggles{All: !m.toggles.All, Net: m.toggles.Net})
	case "n":
		return m.reopen(Toggles{All: m.toggles.All, Net: !m.toggles.Net})
	}
	m.scroll()
	return m, nil
}

func (m Model) typed(msg tea.KeyPressMsg) Model {
	switch msg.Code {
	case tea.KeyEnter:
		m.typing = false
	case tea.KeyEscape:
		m.typing, m.query = false, ""
	case tea.KeyBackspace:
		if runes := []rune(m.query); len(runes) > 0 {
			m.query = string(runes[:len(runes)-1])
		}
	default:
		m.query += msg.Text
	}
	if rows := m.rows(); m.selectedIndex(rows) < 0 && len(rows) > 0 {
		m.selected = rows[0].key
	}
	m.scroll()
	return m
}

func (m *Model) resort() {
	if m.raw == nil {
		return
	}
	projected, err := m.raw.Grouped(m.groupBy)
	if err != nil {
		m.err, m.status = err, err.Error()
		return
	}
	sorted, err := projected.WithGavelTasks().Sorted(m.order)
	if err != nil {
		m.err, m.status = err, err.Error()
		return
	}
	m.frame = &sorted
	if rows := m.rows(); m.selectedIndex(rows) < 0 && len(rows) > 0 {
		m.selected = rows[0].key
	}
}

// reopen reads with toggles from a new source, its frames replacing the
// old one's from its first.
func (m Model) reopen(toggles Toggles) (tea.Model, tea.Cmd) {
	if m.options.Reopen == nil {
		m.status = "a recording reads what it was started with"
		return m, nil
	}
	source, err := m.options.Reopen(toggles)
	if err != nil {
		m.status = fmt.Sprintf("cannot read with all=%t net=%t: %v", toggles.All, toggles.Net, err)
		return m, nil
	}
	m.source.Close()
	m.source, m.toggles = source, toggles
	m.gen++
	m.status = ""
	if toggles.Net {
		m.status = "nettop holds over a core while network bytes are read"
	}
	return m, m.read()
}

// row is a group, or one of an expanded group's processes, as the tree
// shows it. key and parentKey are the tree's: a group's own, or
// proc:<process key> under the row its process nests in.
type row struct {
	group groups.GroupSample
	// process is set on a process row; group is then the group it belongs to.
	process *groups.ProcessSample
	// detached marks a process whose parent is outside its group.
	detached       bool
	key, parentKey string
	depth          int
	parent         bool
	folded         bool
}

const processPrefix = "proc:"

func (m Model) matchingGroups() []groups.GroupSample {
	if m.frame == nil {
		return nil
	}
	if m.query == "" {
		return m.frame.Groups
	}
	byKey := map[string]groups.GroupSample{}
	for _, group := range m.frame.Groups {
		byKey[group.Key] = group
	}
	shown := map[string]bool{}
	query := strings.ToLower(m.query)
	for _, group := range m.frame.Groups {
		match := matches(group, query)
		for parent := byKey[group.ParentKey]; !match && parent.Key != ""; parent = byKey[parent.ParentKey] {
			match = parent.Kind == groups.KindBucket && matches(parent, query)
		}
		if query != "" && !match {
			continue
		}
		for key := group.Key; key != "" && !shown[key]; key = byKey[key].ParentKey {
			shown[key] = true
		}
	}
	var displayed []groups.GroupSample
	for _, group := range m.frame.Groups {
		if shown[group.Key] {
			displayed = append(displayed, group)
		}
	}
	if m.groupBy != groups.GroupTree {
		return groups.Rollup(displayed)
	}
	return displayed
}

// rows applies folding to the matching group forest.
func (m Model) rows() []row {
	displayed := m.matchingGroups()
	parents := map[string]bool{}
	for _, group := range displayed {
		parents[group.ParentKey] = true
	}
	under := m.processesUnder(displayed)
	var rows []row
	depth := map[string]int{}
	hidden := map[string]bool{}
	for _, group := range displayed {
		level, known := depth[group.ParentKey]
		if known {
			level++
		}
		depth[group.Key] = level
		if hidden[group.ParentKey] {
			hidden[group.Key] = true
			continue
		}
		processes := under[group.Key]
		parent := parents[group.Key] || len(processes) > 0
		folded := m.isFolded(group.Key, group, parent)
		hidden[group.Key] = folded
		rows = append(rows, row{group: group, key: group.Key, parentKey: group.ParentKey, depth: level, parent: parent, folded: folded})
		if !folded {
			for _, process := range m.foldProcesses(processes) {
				process.depth += level + 1
				rows = append(rows, process)
			}
		}
	}
	return rows
}

// foldProcesses drops the process rows under a folded process row.
func (m Model) foldProcesses(processes []row) []row {
	var shown []row
	hidden := map[string]bool{}
	for _, r := range processes {
		if hidden[r.parentKey] {
			hidden[r.key] = true
			continue
		}
		r.folded = m.isFolded(r.key, r.group, r.parent)
		hidden[r.key] = r.folded
		shown = append(shown, r)
	}
	return shown
}

func (m Model) isFolded(key string, group groups.GroupSample, parent bool) bool {
	if !parent || m.query != "" {
		return false
	}
	if folded, ok := m.folded[key]; ok {
		return folded
	}
	return group.Kind == groups.KindOther && !group.Total.Hogs.Pinned(m.frame.SampledAt)
}

// toggleExpanded lists or hides the member processes of r's group, or of
// the group a process row belongs to.
func (m *Model) toggleExpanded(r row) {
	key := r.group.Key
	if r.group.Task != nil || r.group.Kind == groups.KindBucket {
		m.status = "enter lists a group's processes; f shows them all below"
		return
	}
	if _, kept := m.source.Processes(groups.ProcessFilter{GroupKey: key}); !kept {
		m.status = "A recording keeps each group's use, not its processes."
		return
	}
	m.expanded[key] = !m.expanded[key]
	if !m.expanded[key] && r.process != nil {
		m.selected = key
	}
}

// processesUnder are the expanded groups' member processes as rows, by the
// key of the row each subtree nests under, their depths counted from it.
// A group's anchor is the group's own row; a member a gavel task runs, and
// the members under it, nest under that task's row.
func (m Model) processesUnder(displayed []groups.GroupSample) map[string][]row {
	under := map[string][]row{}
	for _, group := range displayed {
		if !m.expanded[group.Key] || group.Task != nil || group.Kind == groups.KindBucket {
			continue
		}
		members, _ := m.source.Processes(groups.ProcessFilter{GroupKey: group.Key})
		claims := map[int]string{}
		for _, task := range displayed {
			if task.Task != nil && task.Task.Item != nil && task.Task.Item.PID > 0 && strings.HasPrefix(task.Key, group.Key+"/") {
				claims[task.Task.Item.PID] = task.Key
			}
		}
		for key, rows := range processRows(group, members, claims) {
			under[key] = append(under[key], rows...)
		}
	}
	return under
}

// processRows nests members by parent pid: the anchor's children under the
// group, a claimed process (and those under it) under its task's row, and a
// process whose parent is outside the group directly under it, detached.
// Siblings come busiest first.
func processRows(group groups.GroupSample, members []groups.ProcessSample, claims map[int]string) map[string][]row {
	byPID := map[int]groups.ProcessSample{}
	for _, p := range members {
		if p.PID != group.PID {
			byPID[p.PID] = p
		}
	}
	children := map[int][]groups.ProcessSample{}
	var roots []groups.ProcessSample
	for _, p := range byPID {
		if _, ok := byPID[p.PPID]; ok && claims[p.PID] == "" {
			children[p.PPID] = append(children[p.PPID], p)
		} else {
			roots = append(roots, p)
		}
	}
	busiest := func(processes []groups.ProcessSample) {
		slices.SortFunc(processes, func(a, b groups.ProcessSample) int {
			return cmp.Or(cmp.Compare(cpuOf(b.Usage), cpuOf(a.Usage)), cmp.Compare(a.PID, b.PID))
		})
	}
	under := map[string][]row{}
	var walk func(owner string, p groups.ProcessSample, parentKey string, depth int, detached bool)
	walk = func(owner string, p groups.ProcessSample, parentKey string, depth int, detached bool) {
		process := p
		key := processPrefix + p.Key
		under[owner] = append(under[owner], row{group: group, process: &process, detached: detached, key: key, parentKey: parentKey, depth: depth, parent: len(children[p.PID]) > 0})
		busiest(children[p.PID])
		for _, child := range children[p.PID] {
			walk(owner, child, key, depth+1, false)
		}
	}
	busiest(roots)
	for _, p := range roots {
		if task := claims[p.PID]; task != "" {
			walk(task, p, task, 0, false)
			continue
		}
		walk(group.Key, p, group.Key, 0, p.PPID != group.PID)
	}
	return under
}

func matches(group groups.GroupSample, query string) bool {
	for _, text := range []string{groups.Label(group), group.Command, group.Context.Repo, group.Context.GitCommonDir, group.Context.Session, group.Context.SessionTitle, group.Origin.Label, group.Origin.Service, group.Origin.Owner} {
		if strings.Contains(strings.ToLower(text), query) {
			return true
		}
	}
	return false
}

func (m Model) selectedIndex(rows []row) int {
	return slices.IndexFunc(rows, func(r row) bool { return r.key == m.selected })
}

// selectAt selects the row at index; a process row also chooses its
// process for x and r.
func (m *Model) selectAt(rows []row, index int) {
	if len(rows) == 0 {
		return
	}
	r := rows[max(0, min(index, len(rows)-1))]
	m.selected = r.key
	if r.process != nil {
		m.processKey = r.process.Key
	}
}

// fold folds or unfolds the selected group; folding a group that holds
// none selects its parent instead, as a file tree does.
func (m *Model) fold(rows []row, index int, fold bool) {
	if index < 0 {
		return
	}
	current := rows[index]
	if current.parent && current.folded != fold {
		m.folded[current.key] = fold
		return
	}
	if fold && current.parentKey != "" && slices.ContainsFunc(rows, func(r row) bool { return r.key == current.parentKey }) {
		m.selected = current.parentKey
	}
}

// scroll keeps the selected row within the body.
func (m *Model) scroll() {
	index := m.selectedIndex(m.rows())
	height := m.bodyHeight()
	switch {
	case index < 0 || height <= 0:
	case index < m.offset:
		m.offset = index
	case index >= m.offset+height:
		m.offset = index - height + 1
	}
	m.offset = max(0, m.offset)
}

// selectedProcesses are the processes of the selected group and those
// under it, busiest first.
func (m Model) selectedProcesses() ([]groups.ProcessSample, bool) {
	if m.frame == nil || m.selected == "" {
		return nil, true
	}
	if strings.HasPrefix(m.selected, processPrefix) {
		rows := m.rows()
		if index := m.selectedIndex(rows); index >= 0 {
			return []groups.ProcessSample{*rows[index].process}, true
		}
		return nil, true
	}
	under := map[string]bool{m.selected: true}
	var processes []groups.ProcessSample
	for _, group := range m.matchingGroups() {
		if group.Key != m.selected && !under[group.ParentKey] {
			continue
		}
		under[group.Key] = true
		if group.Kind == groups.KindBucket {
			continue
		}
		read, ok := m.source.Processes(groups.ProcessFilter{GroupKey: group.Key})
		if !ok {
			return nil, false
		}
		processes = append(processes, read...)
	}
	slices.SortStableFunc(processes, func(a, b groups.ProcessSample) int {
		if a.Hogs.Pinned(m.frame.SampledAt) != b.Hogs.Pinned(m.frame.SampledAt) {
			if a.Hogs.Pinned(m.frame.SampledAt) {
				return -1
			}
			return 1
		}
		return cmp.Compare(cpuOf(b.Usage), cpuOf(a.Usage))
	})
	return processes, true
}

func cpuOf(usage groups.Usage) float64 {
	if usage.CPUPercent == nil {
		return 0
	}
	return *usage.CPUPercent
}
