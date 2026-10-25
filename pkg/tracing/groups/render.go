package groups

import (
	"cmp"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/flanksource/clicky/api"
)

// The columns a Frame's siblings are sorted by, by their Total but for the
// name.
const (
	SortCPU       = "cpu"
	SortMemory    = "mem"
	SortIO        = "io"
	SortNet       = "net"
	SortProcesses = "procs"
	SortName      = "name"
)

// Sorts are every column a Frame sorts by.
var Sorts = []string{SortCPU, SortMemory, SortIO, SortNet, SortProcesses, SortName}

// Order is the column siblings are sorted by: a usage column highest first,
// the name A first, unless Reverse turns it.
type Order struct {
	By      string
	Reverse bool
}

// Sorted is the frame's groups as a tree again, each group's children, and
// the top-level groups, sorted in order; ties keep their order. A group
// whose parent is not in the frame is top-level.
func (f Frame) Sorted(order Order) (Frame, error) {
	compare, err := comparer(order)
	if err != nil {
		return Frame{}, err
	}
	present := map[string]bool{}
	for _, group := range f.Groups {
		present[group.Key] = true
	}
	children := map[string][]GroupSample{}
	for _, group := range f.Groups {
		parent := group.ParentKey
		if !present[parent] {
			parent = ""
		}
		children[parent] = append(children[parent], group)
	}
	sorted := f
	sorted.Groups = make([]GroupSample, 0, len(f.Groups))
	var walk func(parent string)
	walk = func(parent string) {
		siblings := children[parent]
		slices.SortStableFunc(siblings, compare)
		for _, group := range siblings {
			sorted.Groups = append(sorted.Groups, group)
			walk(group.Key)
		}
	}
	walk("")
	return sorted, nil
}

func comparer(order Order) (func(a, b GroupSample) int, error) {
	deref := func(value *float64) float64 {
		if value == nil {
			return 0
		}
		return *value
	}
	var value func(Usage) float64
	switch order.By {
	case SortName:
		return func(a, b GroupSample) int {
			if order.Reverse {
				a, b = b, a
			}
			return cmp.Compare(Label(a), Label(b))
		}, nil
	case SortCPU:
		value = func(u Usage) float64 { return deref(u.CPUPercent) }
	case SortMemory:
		value = func(u Usage) float64 { return float64(u.RSSBytes) }
	case SortIO:
		value = func(u Usage) float64 { return deref(u.DiskRead) + deref(u.DiskWrite) }
	case SortNet:
		value = func(u Usage) float64 { return deref(u.NetRx) + deref(u.NetTx) }
	case SortProcesses:
		value = func(u Usage) float64 { return float64(u.Processes) }
	default:
		return nil, fmt.Errorf("sort %q: expected one of %v", order.By, Sorts)
	}
	return func(a, b GroupSample) int {
		if order.Reverse {
			a, b = b, a
		}
		return cmp.Compare(value(b.Total), value(a.Total))
	}, nil
}

// nameWidth is the width of the group column, its tree indent included.
const nameWidth = 44

// Pretty is the frame as a top table: each group's Total, indented under its
// parent, with where it runs and what started it.
func (f Frame) Pretty() api.Text {
	text := api.Text{}.AddText(fmt.Sprintf("%-*s %5s %7s %8s %9s %9s %9s %9s %5s  %-24s %s",
		nameWidth, "GROUP", "PROCS", "CPU%", "RSS", "DISK R/s", "DISK W/s", "NET IN/s", "NET OUT/s", "SOCK", "ORIGIN", "CONTEXT"), "font-bold")
	depth := map[string]int{}
	for _, group := range f.Groups {
		level, ok := depth[group.ParentKey]
		if ok {
			level++
		}
		depth[group.Key] = level
		usage := group.Total
		name := strings.Repeat("  ", level) + Label(group)
		text = text.NewLine().AddText(fmt.Sprintf("%-*s %5d %7s %8s %9s %9s %9s %9s %5s  %-24s ",
			nameWidth, Truncate(name, nameWidth), usage.Processes, Percent(usage.CPUPercent), Size(float64(usage.RSSBytes)),
			Rate(usage.DiskRead), Rate(usage.DiskWrite), Rate(usage.NetRx), Rate(usage.NetTx), Count(usage.Sockets),
			Truncate(OriginText(group.Origin), 24)), kindStyle(group.Kind)).
			AddText(ContextText(group.Context), "text-gray-500")
	}
	return text.NewLine().AddText(fmt.Sprintf("%d groups · tick %d · %s", len(f.Groups), f.Tick, f.SampledAt.Local().Format("15:04:05")), "text-gray-500")
}

// Label names a group: its rule and what it runs against, an agent's
// session cut to 8 characters.
func Label(group GroupSample) string {
	target := group.Target
	if group.Kind == KindAgent && len(target) > 8 {
		target = target[:8]
	}
	return strings.TrimSpace(group.Name + " " + target)
}

func kindStyle(kind string) string {
	switch kind {
	case KindAgent:
		return "text-blue-500"
	case KindTest:
		return "text-green-600"
	case KindBrowser:
		return "text-purple-500"
	case KindOther:
		return "text-gray-500"
	}
	return ""
}

// ContextText is where a group runs: its checkout, agent session and cmux
// workspace, each by name where it has one, else by short id.
func ContextText(c Context) string {
	var parts []string
	if c.Repo != "" {
		parts = append(parts, strings.TrimSuffix(filepath.Base(c.Repo)+"@"+c.Branch, "@"))
	}
	if c.Session != "" {
		parts = append(parts, c.Agent+":"+named(c.SessionTitle, c.Session, 40))
	}
	if c.Workspace != "" {
		parts = append(parts, "cmux:"+named(c.WorkspaceTitle, c.Workspace, 24))
	}
	return strings.Join(parts, " ")
}

// named is title, quoted and cut to width, or the first 8 characters of id
// when there is none.
func named(title, id string, width int) string {
	if title == "" {
		return Truncate(id, 8)
	}
	return strconv.Quote(Truncate(title, width))
}

// OriginText is what started a group's process and, for the system's, who
// runs it: brew:<service>, app:<bundle>, apple, launchd:<label>, brew.
func OriginText(o Origin) string {
	var source string
	switch o.Source {
	case SourceBrewService:
		source = "brew:" + o.Service
	case SourceApp:
		source = "app:" + o.Service
	case SourceLaunchd:
		source = "launchd:" + o.Label
	default:
		source = o.Source
	}
	if o.Scope == ScopeSystem && o.Owner != "" {
		return strings.TrimSpace(source + " " + o.Owner)
	}
	return source
}

// Truncate cuts text to width, ending it with an ellipsis where it cut.
func Truncate(text string, width int) string {
	runes := []rune(text)
	if len(runes) <= width {
		return text
	}
	if width < 1 {
		return ""
	}
	return string(runes[:width-1]) + "…"
}

// Percent is a known percentage to one decimal, or "-".
func Percent(value *float64) string {
	if value == nil {
		return "-"
	}
	return fmt.Sprintf("%.1f", *value)
}

// Rate is a known rate as a Size, or "-".
func Rate(value *float64) string {
	if value == nil {
		return "-"
	}
	return Size(*value)
}

// Count is a known count, or "-".
func Count(value *int) string {
	if value == nil {
		return "-"
	}
	return fmt.Sprintf("%d", *value)
}

// Size is bytes in the largest binary unit that keeps them at least 1.
func Size(bytes float64) string {
	units := []string{"B", "K", "M", "G", "T"}
	unit := 0
	for bytes >= 1024 && unit < len(units)-1 {
		bytes /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%.0f%s", bytes, units[unit])
	}
	return fmt.Sprintf("%.1f%s", bytes, units[unit])
}
