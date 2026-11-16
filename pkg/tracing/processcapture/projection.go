package processcapture

import "github.com/flanksource/telltale/pkg/tracing/process"

// Nodes projects startup identities and numeric history into a current tree.
func Nodes(identities []Identity, metrics []MetricRow) []process.Node {
	index := map[string]int{}
	nodes := make([]process.Node, 0, len(identities))
	for _, p := range identities {
		status := "running"
		if p.ExitedAt != nil {
			status = "exited"
		}
		index[p.Key] = len(nodes)
		nodes = append(nodes, process.Node{Key: p.Key, ParentKey: p.ParentKey, PID: p.PID, PPID: p.PPID, Name: p.Name, Command: p.Command, CWD: p.CWD, Status: status,
			StartedAt: p.StartedAt, ExitedAt: p.ExitedAt, LastSeenAt: p.FirstObservedAt, IsRoot: p.ParentKey == "", Environment: p.Environment, EnvironmentError: p.EnvironmentError, StackTransports: p.StackTransports})
	}
	for _, sample := range metrics {
		i, ok := index[sample.Entity]
		if !ok {
			continue
		}
		n := &nodes[i]
		if !sample.At.Before(n.LastSeenAt) {
			n.CPUPercent, n.RSSBytes, n.LastSeenAt = sample.CPUPercent, sample.RSSBytes, sample.At
		}
		n.PeakRSSBytes = max(n.PeakRSSBytes, sample.RSSBytes)
		if sample.CPUPercent != nil && (n.PeakCPUPercent == nil || *sample.CPUPercent > *n.PeakCPUPercent) {
			n.PeakCPUPercent = sample.CPUPercent
		}
	}
	return nodes
}

// Samples joins each metric row to its process identity for SDK consumers.
func Samples(identities []Identity, metrics []MetricRow) []process.Sample {
	byKey := map[string]Identity{}
	for _, p := range identities {
		byKey[p.Key] = p
	}
	samples := make([]process.Sample, 0, len(metrics))
	for _, row := range metrics {
		p, ok := byKey[row.Entity]
		if !ok {
			continue
		}
		var connections *int
		if row.ConnectionsIn != nil && row.ConnectionsOut != nil && row.ConnectionsUnknown != nil {
			total := *row.ConnectionsIn + *row.ConnectionsOut + *row.ConnectionsUnknown
			connections = &total
		}
		samples = append(samples, process.Sample{SampledAt: row.At, Tick: row.Tick, Key: row.Entity, PID: p.PID, CPUPercent: row.CPUPercent, MemoryPercent: row.MemoryPercent, RSSBytes: row.RSSBytes,
			DiskReadBytes: row.DiskReadBytes, DiskWriteBytes: row.DiskWriteBytes, OpenFiles: row.OpenFiles, Ports: row.ReceivingSockets, Sockets: row.ReceivingSockets, Connections: connections})
	}
	return samples
}
