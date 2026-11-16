package processcapture

import (
	"fmt"

	"github.com/flanksource/clicky"
	"github.com/flanksource/clicky/api"
)

func (r Identity) Pretty() api.Text {
	t := clicky.Text(fmt.Sprintf("pid %d", r.PID), "text-muted").Space().Append(r.Name, "text-blue-500 font-bold").Space().Append(r.Command, "font-mono")
	if r.ExitedAt != nil {
		t = t.Space().Append("exited", "text-yellow-500")
	}
	if r.EnvironmentError != "" {
		t = t.Space().Append(r.EnvironmentError, "text-red-500")
	}
	return t
}

func (r Identity) PrettyFull() api.Textable {
	return r.Pretty().NewLine().Add(clicky.Map(r.Tags)).NewLine().Add(clicky.Map(map[string]any{"groups": r.Groups, "cwd": r.CWD, "parent": r.ParentKey}))
}

func (r MetricRow) Pretty() api.Text {
	t := clicky.Text(r.Entity, "text-muted")
	if r.CPUPercent != nil {
		t = t.Space().Append(fmt.Sprintf("CPU %.1f%%", *r.CPUPercent), "text-blue-500")
	}
	t = t.Space().Append("RSS ", "text-muted").Add(api.HumanizeBytes(int64(r.RSSBytes)))
	if r.OpenFiles != nil {
		t = t.Space().Append(fmt.Sprintf("%d files", *r.OpenFiles), "text-muted")
	}
	return t
}

func (r MetricRow) PrettyFull() api.Textable {
	return r.Pretty().NewLine().Add(clicky.Map(map[string]any{
		"CPU %": r.CPUPercent, "RSS": api.HumanizeBytes(int64(r.RSSBytes)), "Files": r.OpenFiles,
		"Disk read bytes": r.DiskReadBytes, "Disk write bytes": r.DiskWriteBytes,
		"Disk read B/s": r.DiskReadBps, "Disk write B/s": r.DiskWriteBps,
		"Network receive B/s": r.NetRxBps, "Network send B/s": r.NetTxBps,
		"Connections in": r.ConnectionsIn, "Connections out": r.ConnectionsOut,
	}))
}

func (r FacetEvent) Pretty() api.Text {
	name := r.Entity
	if r.Resource.Name != "" {
		name = r.Resource.Name
	}
	return clicky.Text(r.Process, "text-muted").Space().Append(r.Facet, "text-blue-500").
		Space().Append(r.Operation, "text-yellow-500").Space().Append(name, "font-mono")
}

func (r FacetEvent) PrettyFull() api.Textable {
	return r.Pretty().NewLine().Add(clicky.Map(map[string]any{"resource": r.Resource, "reason": r.Reason, "directionSource": r.DirectionSource}))
}
