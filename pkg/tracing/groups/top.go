package groups

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	clickyprocess "github.com/flanksource/clicky/process"
	"github.com/flanksource/telltale/pkg/tracing/process"
)

// GroupSample is one group at one tick: what it is, where it runs, and what
// it and the groups under it use.
type GroupSample struct {
	SampledAt time.Time `json:"sampledAt" pretty:"label=Sampled" sort:"sampledAt"`
	Tick      int64     `json:"tick" sort:"tick"`
	Key       string    `json:"key" pretty:"hide" filter:"exact"`
	ParentKey string    `json:"parentKey,omitempty" pretty:"hide" filter:"exact"`
	Name      string    `json:"name" filter:"terms"`
	Kind      string    `json:"kind" filter:"terms"`
	Target    string    `json:"target,omitempty"`
	PID       int       `json:"pid" pretty:"label=PID" filter:"terms"`
	Command   string    `json:"command" filter:"text"`
	Context   Context   `json:"context" filter:"none"`
	Origin    Origin    `json:"origin" filter:"none"`
	Self      Usage     `json:"self" filter:"none"`
	Total     Usage     `json:"total" filter:"none"`
}

// Frame is every group shown at one tick, parents before their children,
// and the host's own use when read live.
type Frame struct {
	SampledAt time.Time  `json:"sampledAt"`
	Tick      int64      `json:"tick"`
	Host      *HostUsage `json:"host,omitempty"`
	// Overhead is what reading the frame cost, when read live.
	Overhead *Overhead     `json:"overhead,omitempty"`
	Groups   []GroupSample `json:"groups"`
}

// ProcessSample is one process at one tick: who runs it, the group it is in,
// and what it uses.
type ProcessSample struct {
	Key      string `json:"key"`
	PID      int    `json:"pid"`
	PPID     int    `json:"ppid"`
	Name     string `json:"name"`
	Command  string `json:"command"`
	GroupKey string `json:"groupKey"`
	Origin   Origin `json:"origin"`
	Usage
}

// ProcessFilter picks processes: those of a group, those running a
// program, or a process and every one under it.
type ProcessFilter struct {
	GroupKey string
	Name     string
	PID      int
}

// Options choose what a Top reads and shows.
type Options struct {
	Catalog Catalog
	// All shows, and reads the disk and descriptors of, the processes in no
	// tool's group too.
	All bool
	// Groups shows only the groups of these rules, and those under them.
	Groups []string
	// Net reads each process's network bytes with nettop, every Interval.
	Net      bool
	Interval time.Duration
}

// Top reads the host's process groups at each Tick.
type Top struct {
	mu        sync.Mutex
	sampler   *process.Sampler
	options   Options
	rates     *Rates
	contexts  *contexts
	launchd   launchd
	brew      []string
	self      int
	overhead  overheadReader
	host      hostReader
	net       *NetMonitor
	tick      int64
	processes []ProcessSample
}

// NewTop is a Top over sampler, refusing a group no rule names; with Net it
// starts nettop, which Close stops.
func NewTop(ctx context.Context, sampler *process.Sampler, options Options) (*Top, error) {
	for _, name := range options.Groups {
		if _, ok := options.Catalog.Rule(name); !ok {
			return nil, fmt.Errorf("group %q is no rule of the catalog", name)
		}
	}
	top := &Top{sampler: sampler, options: options, rates: NewRates(), contexts: newContexts(), brew: BrewPrefixes(), self: os.Getpid()}
	if options.Net {
		monitor, err := StartNetMonitor(ctx, options.Interval)
		if err != nil {
			return nil, err
		}
		top.net = monitor
	}
	return top, nil
}

// Ready is closed once every reading the Top was asked for can be rated:
// at once, but for network bytes, once nettop has counted past its baseline.
func (t *Top) Ready() <-chan struct{} {
	if t.net != nil {
		return t.net.Ready()
	}
	ready := make(chan struct{})
	close(ready)
	return ready
}

// Close stops what the Top runs.
func (t *Top) Close() {
	if t.net != nil {
		t.net.Close()
	}
}

// Tick lists the host, sorts it into groups and reads what each uses.
func (t *Top) Tick(ctx context.Context) (Frame, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	started := time.Now()
	listing, at, err := t.sampler.Observe(ctx, 0)
	if err != nil {
		return Frame{}, err
	}
	jobs, err := t.launchd.read(ctx)
	if err != nil {
		return Frame{}, err
	}
	origins := make(map[string]Origin, len(listing))
	for _, p := range listing {
		origins[keyOf(p)] = OriginOf(p.Process, jobs, t.brew)
	}
	classified := Classify(listing, ClassifyOptions{Catalog: t.options.Catalog, Origins: origins, Self: t.self})
	readings, held, err := t.read(ctx, classified, at)
	if err != nil {
		return Frame{}, err
	}
	host, err := t.host.read(ctx)
	if err != nil {
		return Frame{}, err
	}
	totals := Aggregate(classified, readings)
	contexts := t.contexts.of(ctx, classified, held)
	t.tick++
	t.processes = t.processes[:0]
	for _, group := range classified {
		for _, member := range group.Members {
			key := keyOf(member)
			var usage Usage
			usage.add(readings[key])
			t.processes = append(t.processes, ProcessSample{
				Key: key, PID: member.PID, PPID: member.PPID, Name: program(strings.Fields(member.Command)), Command: member.Command,
				GroupKey: group.Key, Origin: origins[key], Usage: usage,
			})
		}
	}
	overhead, err := t.overhead.read(started, time.Now())
	if err != nil {
		return Frame{}, err
	}
	frame := Frame{SampledAt: at, Tick: t.tick, Host: &host, Overhead: &overhead}
	for _, group := range t.shown(classified) {
		frame.Groups = append(frame.Groups, GroupSample{
			SampledAt: at, Tick: t.tick, Key: group.Key, ParentKey: group.ParentKey,
			Name: group.Rule.Name, Kind: group.Rule.Kind, Target: group.Target,
			PID: group.Anchor.PID, Command: group.Anchor.Command, Context: contexts[group.Key], Origin: group.Origin,
			Self: totals[group.Key].Self, Total: totals[group.Key].Total,
		})
	}
	return frame, nil
}

// Processes are the processes of the latest Tick that filter picks, by pid.
func (t *Top) Processes(filter ProcessFilter) []ProcessSample {
	t.mu.Lock()
	defer t.mu.Unlock()
	picked := map[int]bool{}
	if filter.PID != 0 {
		picked[filter.PID] = true
		// Parents may be listed after their children: grow the subtree until it holds.
		for grew := true; grew; {
			grew = false
			for _, p := range t.processes {
				if picked[p.PPID] && !picked[p.PID] && p.PID != p.PPID {
					picked[p.PID], grew = true, true
				}
			}
		}
	}
	var result []ProcessSample
	for _, p := range t.processes {
		switch {
		case filter.PID != 0 && !picked[p.PID]:
		case filter.GroupKey != "" && p.GroupKey != filter.GroupKey:
		case filter.Name != "" && p.Name != filter.Name:
		default:
			result = append(result, p)
		}
	}
	slices.SortFunc(result, func(a, b ProcessSample) int { return cmp.Compare(a.PID, b.PID) })
	return result
}

// read is each process's reading by key: the CPU and memory of every one,
// and the disk, descriptors and network of those measured; and the
// descriptors each measured process holds, by pid.
func (t *Top) read(ctx context.Context, classified []Group, at time.Time) (map[string]Reading, map[int][]clickyprocess.Descriptor, error) {
	var measured []clickyprocess.Process
	readings := map[string]Reading{}
	alive := map[string]bool{}
	for _, group := range classified {
		for _, member := range group.Members {
			key := keyOf(member)
			alive[key] = true
			readings[key] = Reading{CPUPercent: member.IntervalCPUPercent, RSSBytes: member.RSSBytes}
			if t.options.All || group.Rule.Kind != KindOther {
				measured = append(measured, member.Process)
			}
		}
	}
	t.rates.Forget(alive)
	pids := make([]int, 0, len(measured))
	for _, p := range measured {
		pids = append(pids, p.PID)
	}
	snapshot := clickyprocess.NewSnapshot(measured)
	if err := snapshot.PopulateIO(ctx, pids); err != nil {
		return nil, nil, err
	}
	descriptors, err := clickyprocess.Descriptors(ctx, pids...)
	if err != nil {
		return nil, nil, fmt.Errorf("read sockets: %w", err)
	}
	var network map[int]NetBytes // nil leaves every process's network unknown
	if t.net != nil {
		if network, err = t.net.Counters(); err != nil {
			return nil, nil, err
		}
	}
	for _, p := range snapshot.All() {
		key := keyOf(process.Observed{Process: p})
		reading := readings[key]
		var counters Counters
		if p.IO != nil {
			counters.DiskRead, counters.DiskWrite = &p.IO.DiskReadBytes, &p.IO.DiskWriteBytes
		}
		if network != nil {
			// nettop lists only the processes with flows: one it does not list sent nothing.
			sent := network[p.PID]
			counters.NetRx, counters.NetTx = &sent.Rx, &sent.Tx
		}
		reading.PerSecond = t.rates.Observe(key, at, counters)
		if held, ok := descriptors[p.PID]; ok {
			reading.Sockets, reading.Connections = countOf(held, clickyprocess.Descriptor.IsSocket), countOf(held, clickyprocess.Descriptor.IsConnected)
		}
		readings[key] = reading
	}
	return readings, descriptors, nil
}

func countOf(held []clickyprocess.Descriptor, holds func(clickyprocess.Descriptor) bool) *int {
	count := 0
	for _, descriptor := range held {
		if holds(descriptor) {
			count++
		}
	}
	return &count
}

// shown are the groups the options show: no KindOther ones unless All, and
// with Groups only theirs and those under them.
func (t *Top) shown(classified []Group) []Group {
	kept := map[string]bool{}
	var shown []Group
	for _, group := range classified {
		switch {
		case group.Rule.Kind == KindOther && !t.options.All:
			continue
		case len(t.options.Groups) > 0 && !slices.Contains(t.options.Groups, group.Rule.Name) && !kept[group.ParentKey]:
			continue
		}
		kept[group.Key] = true
		shown = append(shown, group)
	}
	return shown
}

func keyOf(p process.Observed) string { return process.Key(p.Process) }

// CatalogFile is the groups catalog a work directory keeps, at
// .telltale/groups.yaml, or "" when it keeps none.
func CatalogFile(workDir string) (string, error) {
	path := filepath.Join(workDir, ".telltale", "groups.yaml")
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("groups catalog: %w", err)
	}
	return path, nil
}
