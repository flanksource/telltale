package agentclient

import (
	"maps"
	"slices"
	"strings"

	"github.com/flanksource/clicky"
	"github.com/flanksource/clicky/api"
)

func (groups Groups) Pretty() api.Text {
	if len(groups) == 0 {
		return clicky.Text("No agents found.", "muted")
	}
	text := api.Text{}
	for _, group := range slices.Sorted(maps.Keys(groups)) {
		text = text.NewLine().AddText(group, "font-bold").NewLine().Add(api.NewTableFrom(groups[group]))
	}
	return text
}

func (i Info) Columns() []api.ColumnDef {
	return []api.ColumnDef{
		clicky.Column("agentId").Label("Agent").MaxWidth(60).Build(),
		clicky.Column("runtime").Label("Runtime").Build(),
		clicky.Column("connection").Label("Connection").Build(),
		clicky.Column("deployment").Label("Deployment").Build(),
		clicky.Column("endpoint").Label("Endpoint").MaxWidth(60).Build(),
	}
}

func (i Info) Row() map[string]any {
	row := map[string]any{"agentId": i.AgentID, "connection": "Not connected"}
	if i.Connected {
		row["connection"] = "Connected"
	}
	if i.Hello != nil {
		row["runtime"] = strings.TrimSpace(i.Runtime.Lang + " " + i.Runtime.Version)
	}
	if i.Known != nil {
		row["runtime"] = i.Known.Runtime
		row["deployment"] = i.Known.State
		row["endpoint"] = i.Known.Endpoint
	}
	return row
}
