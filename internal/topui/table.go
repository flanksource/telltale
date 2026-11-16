package topui

import (
	"fmt"
	"strings"

	"github.com/flanksource/clicky"
	"github.com/flanksource/clicky/api"
	"github.com/flanksource/clicky/tui/table"
	"github.com/flanksource/telltale/pkg/tracing/groups"
)

type processTableRow struct {
	command         string
	name            string
	usage           groups.Usage
	origin, context string
}

func (r processTableRow) RowDetail() api.Textable {
	return clicky.Map(map[string]any{"Name": r.name, "Command": r.command, "Usage": r.usage, "Origin": r.origin, "Context": r.context})
}

func (processTableRow) Columns() []api.ColumnDef {
	return []api.ColumnDef{api.Column("name").Label("GROUP / PROCESS").Build(), api.Column("cpu").Label("CPU%").Build(), api.Column("rss").Label("RSS").Build(), api.Column("processes").Label("PROCS").Build(), api.Column("compressed").Label("CMPRS").Build(), api.Column("swap").Label("SWAP").Build(), api.Column("read").Label("DISK R/s").Build(), api.Column("write").Label("DISK W/s").Build(), api.Column("netRx").Label("NET RX/s").Build(), api.Column("netTx").Label("NET TX/s").Build(), api.Column("sockets").Label("SOCK").Build(), api.Column("connections").Label("CONN").Build(), api.Column("origin").Label("ORIGIN").Build(), api.Column("context").Label("CONTEXT").Build()}
}
func (r processTableRow) Row() map[string]any {
	return map[string]any{"name": r.name, "cpu": api.TableCell{Value: groups.Percent(r.usage.CPUPercent), FilterValue: r.usage.CPUPercent}, "rss": api.TableCell{Value: groups.Size(float64(r.usage.RSSBytes)), FilterValue: r.usage.RSSBytes}, "processes": r.usage.Processes, "compressed": groups.MemorySize(r.usage.CompressedBytes), "swap": groups.MemorySize(r.usage.SwapBytes), "read": groups.Rate(r.usage.DiskRead), "write": groups.Rate(r.usage.DiskWrite), "netRx": groups.Rate(r.usage.NetRx), "netTx": groups.Rate(r.usage.NetTx), "sockets": r.usage.Sockets, "connections": r.usage.Connections, "origin": r.origin, "context": r.context}
}

func (m Model) processTable() table.Model {
	rows, ids := []processTableRow{}, []string{}
	for _, r := range m.rows() {
		usage, label := r.group.Total, groups.Label(r.group)
		if r.process != nil {
			usage, label = r.process.Usage, fmt.Sprintf("%d %s", r.process.PID, groups.ShortCommand(r.process.Command))
		}
		if r.group.Task != nil {
			label = groups.TaskText(r.group, m.frame.SampledAt)
		}
		if r.process == nil && r.group.Task == nil {
			if slots := groups.SlotGlyphs(r.group.Gavel); slots != "" {
				label += "  " + slots
			}
			if r.group.GavelError != "" {
				label += " · status unreadable"
			}
		}
		glyph := "  "
		if r.parent {
			glyph = "▾ "
			if r.folded {
				glyph = "▸ "
			}
		}
		if r.detached {
			glyph = "↰ "
		}
		if hog := m.hogLabel(usage); hog != "" {
			label = "! " + hog + " · " + label
		}
		command := r.group.Command
		if r.process != nil {
			command = r.process.Command
		}
		rows = append(rows, processTableRow{command: command, name: strings.Repeat("  ", r.depth) + glyph + label, usage: usage, origin: groups.OriginText(r.group.Origin), context: groups.ContextText(r.group.Context)})
		ids = append(ids, r.key)
	}
	model := table.Model{Selected: m.selected}
	if err := model.Set(api.NewTableFrom(rows), ids); err != nil {
		panic(err)
	}
	return model
}
