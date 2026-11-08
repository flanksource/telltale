package processcapture

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	clickyprocess "github.com/flanksource/clicky/process"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/groups"
	"github.com/flanksource/telltale/pkg/tracing/process"
)

type Follower struct {
	mu      sync.Mutex
	runtime *tracing.Runtime
	ref     tracing.TraceRef
	after   int64
	latest  groups.Snapshot
}

func NewFollower(runtime *tracing.Runtime, ref tracing.TraceRef) (*Follower, error) {
	if runtime == nil || ref.Kind != Kind {
		return nil, fmt.Errorf("process follower requires a process capture and runtime")
	}
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	return &Follower{runtime: runtime, ref: ref}, nil
}

// Frame derives the latest committed observation from identities and metrics.
func (f *Follower) Frame(ctx context.Context) (groups.Frame, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ref := tracing.TraceRef{Kind: MetricsTable, Stream: f.ref.Stream + "-metrics", AfterSeq: f.after}
	meta, err := f.runtime.Results.Backend.Meta(ctx, ref.Stream)
	if err != nil {
		return groups.Frame{}, false, err
	}
	if meta.HighSeq == f.after {
		return f.latest.Frame, f.latest.Frame.Tick > 0, nil
	}
	ref.ToSeq = meta.HighSeq
	var identities []Identity
	if err := tracing.ReadRecords(ctx, f.runtime, f.ref, func(p Identity) error { identities = append(identities, p); return nil }); err != nil {
		return groups.Frame{}, false, err
	}
	var rows []MetricRow
	var latest int64
	if err := tracing.ReadRecords(ctx, f.runtime, ref, func(row MetricRow) error {
		if row.Tick > latest {
			rows, latest = rows[:0], row.Tick
		}
		if row.Tick == latest {
			rows = append(rows, row)
		}
		return nil
	}); err != nil {
		return groups.Frame{}, false, err
	}
	snapshot, err := GroupSnapshot(identities, rows)
	if err != nil {
		return groups.Frame{}, false, err
	}
	f.after, f.latest = meta.HighSeq, snapshot
	return snapshot.Frame, snapshot.Frame.Tick > 0, nil
}

func (f *Follower) Processes(filter groups.ProcessFilter) []groups.ProcessSample {
	f.mu.Lock()
	defer f.mu.Unlock()
	return groups.FilterProcesses(f.latest.Processes, filter)
}

// GroupSnapshot aggregates process measurements using startup grouping tags.
func GroupSnapshot(identities []Identity, rows []MetricRow) (groups.Snapshot, error) {
	byKey := map[string]Identity{}
	for _, p := range identities {
		byKey[p.Key] = p
	}
	definitions := map[string]GroupInfo{}
	classified := []groups.Group{}
	indices := map[string]int{}
	readings := map[string]groups.Reading{}
	result := groups.Snapshot{}
	for _, row := range rows {
		p, ok := byKey[row.Entity]
		if !ok {
			return result, fmt.Errorf("metrics reference unknown process %q", row.Entity)
		}
		if len(p.Groups) == 0 {
			return result, fmt.Errorf("process %s has no startup grouping tags", p.Key)
		}
		for _, info := range slices.Backward(p.Groups) {
			if _, exists := definitions[info.Key]; exists {
				continue
			}
			definitions[info.Key] = info
			indices[info.Key] = len(classified)
			classified = append(classified, groups.Group{Key: info.Key, ParentKey: info.ParentKey})
		}
		observed := process.Observed{Process: clickyprocess.Process{PID: p.PID, PPID: p.PPID, Command: p.Command}}
		if strings.Contains(p.Key, "@") {
			observed.StartedAt = &p.StartedAt
		}
		if process.Key(observed.Process) != p.Key {
			return result, fmt.Errorf("process identity %q does not match its pid and start", p.Key)
		}
		index := indices[p.Groups[0].Key]
		classified[index].Members = append(classified[index].Members, observed)
		var connections *int
		if row.ConnectionsIn != nil && row.ConnectionsOut != nil && row.ConnectionsUnknown != nil {
			n := *row.ConnectionsIn + *row.ConnectionsOut + *row.ConnectionsUnknown
			connections = &n
		}
		reading := groups.Reading{CPUPercent: row.CPUPercent, RSSBytes: row.RSSBytes, SwapBytes: row.SwapBytes, CompressedBytes: row.CompressedBytes, Sockets: row.ReceivingSockets, Connections: connections,
			PerSecond: groups.PerSecond{DiskRead: row.DiskReadBps, DiskWrite: row.DiskWriteBps, NetRx: row.NetRxBps, NetTx: row.NetTxBps}}
		readings[p.Key] = reading
		result.Processes = append(result.Processes, groups.ProcessSample{Key: p.Key, PID: p.PID, PPID: p.PPID, Name: p.Name, Command: p.Command, GroupKey: p.Groups[0].Key,
			Origin: groups.Origin{Owner: p.Tags["owner"], Scope: p.Tags["scope"], Source: p.Tags["source"], Service: p.Tags["service"]},
			Usage:  groups.Usage{Processes: 1, CPUPercent: reading.CPUPercent, RSSBytes: reading.RSSBytes, SwapBytes: reading.SwapBytes, CompressedBytes: reading.CompressedBytes, PerSecond: reading.PerSecond, Sockets: reading.Sockets, Connections: reading.Connections}})
		result.Frame.SampledAt, result.Frame.Tick = row.At, row.Tick
	}
	totals := groups.Aggregate(classified, readings)
	for _, group := range classified {
		info := definitions[group.Key]
		result.Frame.Groups = append(result.Frame.Groups, groups.GroupSample{SampledAt: result.Frame.SampledAt, Tick: result.Frame.Tick, Key: info.Key, ParentKey: info.ParentKey, Name: info.Name, Kind: info.Kind, Target: info.Target, PID: info.PID, Command: info.Command, Context: info.Context, Origin: info.Origin, SkipAncestry: info.SkipAncestry,
			Self: totals[info.Key].Self, Total: totals[info.Key].Total})
	}
	return result, nil
}
