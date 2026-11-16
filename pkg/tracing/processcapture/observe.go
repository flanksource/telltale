package processcapture

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	clickyprocess "github.com/flanksource/clicky/process"
	"github.com/flanksource/commons-db/recordstore/metrics"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/groups"
	"github.com/flanksource/telltale/pkg/tracing/process"
)

type MetricRow struct {
	Entity             string    `json:"entity" filter:"exact"`
	At                 time.Time `json:"observedAt"`
	Tick               int64     `json:"tick"`
	CPUPercent         *float64  `json:"cpuPercent" pretty:"unit=percent"`
	RSSBytes           uint64    `json:"rssBytes" pretty:"unit=bytes"`
	MemoryPercent      float64   `json:"memoryPercent" pretty:"unit=percent"`
	DiskReadBytes      *uint64   `json:"diskReadBytes" pretty:"unit=bytes"`
	DiskWriteBytes     *uint64   `json:"diskWriteBytes" pretty:"unit=bytes"`
	OpenFiles          *int      `json:"openFiles"`
	ReceivingSockets   *int      `json:"receivingSockets"`
	ConnectionsIn      *int      `json:"connectionsIn"`
	ConnectionsOut     *int      `json:"connectionsOut"`
	ConnectionsUnknown *int      `json:"connectionsUnknown"`
	DiskReadBps        *float64  `json:"diskReadBps" pretty:"unit=Bps"`
	DiskWriteBps       *float64  `json:"diskWriteBps" pretty:"unit=Bps"`
	NetRxBps           *float64  `json:"netRxBps" pretty:"unit=Bps"`
	NetTxBps           *float64  `json:"netTxBps" pretty:"unit=Bps"`
	SwapBytes          *uint64   `json:"swapBytes" pretty:"unit=bytes"`
	CompressedBytes    *uint64   `json:"compressedBytes" pretty:"unit=bytes"`
}

func (r *capture) observe(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	snapshot, err := r.top.Snapshot(ctx)
	if err != nil {
		return err
	}
	o := Observation{At: snapshot.Frame.SampledAt, Tick: r.store.tick + 1}
	keys := map[int]string{}
	for _, p := range snapshot.Observed {
		keys[p.PID] = process.Key(p.Process)
	}
	root := r.params.root()
	r.rootGone = root != 0 && keys[root] == ""
	tags := groupingTags(snapshot)
	paths := groupPaths(snapshot)
	usage := map[string]groups.Usage{}
	for _, p := range snapshot.Processes {
		usage[p.Key] = p.Usage
	}
	var fresh []clickyprocess.Process
	var pids []int
	for _, p := range snapshot.Observed {
		if _, ok := r.store.identities[keys[p.PID]]; !ok {
			fresh = append(fresh, p.Process)
			pids = append(pids, p.PID)
		}
	}
	startup := clickyprocess.NewSnapshot(fresh)
	startup.PopulateWorkingDirectories(ctx, pids)
	for _, p := range snapshot.Observed {
		key := keys[p.PID]
		if _, ok := r.store.identities[key]; !ok {
			initial, _ := startup.Get(p.PID)
			fields := strings.Fields(p.Command)
			if len(fields) == 0 {
				return fmt.Errorf("process %s has no command", key)
			}
			identity := Identity{Key: key, ParentKey: keys[p.PPID], PID: p.PID, PPID: p.PPID, Name: filepath.Base(fields[0]), Command: p.Command, CWD: initial.CWD, FirstObservedAt: o.At, StartedAt: o.At, Tags: tags[key], Groups: paths[key]}
			if p.StartedAt != nil {
				identity.StartedAt = *p.StartedAt
			}
			if r.dumpers != nil {
				identity.StackTransports = r.dumpers.Transports(p.PID)
			}
			if r.params.Environment != nil {
				environment, err := clickyprocess.Environment(ctx, p.PID, clickyprocess.EnvironmentOptions{Keys: r.params.Environment.Keys, Prefixes: r.params.Environment.Prefixes})
				if err != nil {
					identity.EnvironmentError = err.Error()
				} else {
					identity.Environment = tracing.MaskEnvironment(environment)
				}
			}
			o.Processes = append(o.Processes, identity)
		}
		values := map[string]any{"rssBytes": p.RSSBytes, "memoryPercent": p.MemoryPercent, "cpuPercent": nil, "diskReadBytes": nil, "diskWriteBytes": nil}
		if p.IntervalCPUPercent != nil {
			values["cpuPercent"] = *p.IntervalCPUPercent
		}
		if reading := snapshot.IO[p.PID]; reading.IO != nil {
			values["diskReadBytes"], values["diskWriteBytes"] = reading.IO.DiskReadBytes, reading.IO.DiskWriteBytes
		}
		u := usage[key]
		for name, value := range map[string]*float64{"diskReadBps": u.DiskRead, "diskWriteBps": u.DiskWrite, "netRxBps": u.NetRx, "netTxBps": u.NetTx} {
			values[name] = nil
			if value != nil {
				values[name] = *value
			}
		}
		for name, value := range map[string]*uint64{"swapBytes": u.SwapBytes, "compressedBytes": u.CompressedBytes} {
			values[name] = nil
			if value != nil {
				values[name] = *value
			}
		}
		o.Metrics = append(o.Metrics, metrics.Sample{Entity: key, Values: values})
		if len(r.params.Facets) > 0 {
			held, complete := snapshot.Descriptors[p.PID]
			o.Facets = append(o.Facets, FacetObservation{Process: key, Complete: complete, Descriptors: held, Enabled: r.params.Facets})
		}
	}
	// A process outside the selected subtree may have been reparented. Only a
	// successful host listing can establish that its identity is no longer alive.
	all, _, err := r.sampler.Observe(ctx, 0)
	if err != nil {
		return err
	}
	alive := map[string]bool{}
	for _, p := range all {
		alive[process.Key(p.Process)] = true
	}
	for key, identity := range r.store.identities {
		if identity.ExitedAt == nil && !alive[key] {
			identity.ExitedAt = &o.At
			o.Processes = append(o.Processes, identity)
			o.Facets = append(o.Facets, FacetObservation{Process: key, Exited: true})
		}
	}
	return r.store.Apply(ctx, o)
}

func groupPaths(snapshot groups.Snapshot) map[string][]GroupInfo {
	byKey := map[string]groups.GroupSample{}
	for _, g := range snapshot.Frame.Groups {
		byKey[g.Key] = g
	}
	paths := map[string][]GroupInfo{}
	for _, p := range snapshot.Processes {
		for key := p.GroupKey; key != ""; {
			g, ok := byKey[key]
			if !ok {
				break
			}
			paths[p.Key] = append(paths[p.Key], GroupInfo{Key: g.Key, ParentKey: g.ParentKey, Name: g.Name, Kind: g.Kind, Target: g.Target, PID: g.PID, Command: g.Command, Context: g.Context, Origin: g.Origin, SkipAncestry: g.SkipAncestry})
			key = g.ParentKey
		}
	}
	return paths
}

func groupingTags(snapshot groups.Snapshot) map[string]map[string]string {
	byGroup := map[string]groups.GroupSample{}
	for _, group := range snapshot.Frame.Groups {
		byGroup[group.Key] = group
	}
	result := map[string]map[string]string{}
	for _, p := range snapshot.Processes {
		g := byGroup[p.GroupKey]
		result[p.Key] = map[string]string{"group": g.Key, "groupParent": g.ParentKey, "groupName": g.Name, "groupKind": g.Kind, "groupPID": strconv.Itoa(g.PID), "groupCommand": g.Command, "target": g.Target,
			"owner": p.Origin.Owner, "scope": p.Origin.Scope, "source": p.Origin.Source, "service": p.Origin.Service,
			"repo": g.Context.Repo, "branch": g.Context.Branch, "cwd": g.Context.CWD, "agent": g.Context.Agent, "session": g.Context.Session, "workspace": g.Context.Workspace, "surface": g.Context.Surface}
	}
	return result
}
