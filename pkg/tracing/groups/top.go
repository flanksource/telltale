package groups

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	clickyprocess "github.com/flanksource/clicky/process"
	"github.com/flanksource/gavel/sdk/status"
	"github.com/flanksource/telltale/pkg/datadir"
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
	// Gavel is the progress and slots a gavel group's process publishes, and
	// GavelError why it could not be read.
	Gavel      *status.Status `json:"gavel,omitempty" filter:"none" pretty:"hide"`
	GavelError string         `json:"gavelError,omitempty" filter:"none" pretty:"hide"`
	// SkipAncestry is the group's rule's: its tasks claim their processes'
	// groups by pid before ancestry does.
	SkipAncestry bool `json:"skipAncestry,omitempty" filter:"none" pretty:"hide"`
	// Task is what a row WithGavelTasks adds shows: a task group's progress,
	// or one running task. It is never recorded.
	Task *GavelTask `json:"-" filter:"none" pretty:"hide"`
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
	// Slots are the gavel slot pools the gavel groups hold or wait for.
	Slots []SlotPool `json:"slots,omitempty"`
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
// program, a process and every one under it, or those whose name, command
// or pid holds the Query text, case-insensitively.
type ProcessFilter struct {
	GroupKey string
	Name     string
	PID      int
	Query    string
}

func (p ProcessSample) holds(query string) bool {
	query = strings.ToLower(query)
	return strings.Contains(strings.ToLower(p.Name), query) ||
		strings.Contains(strings.ToLower(p.Command), query) ||
		strings.Contains(strconv.Itoa(p.PID), query)
}

// TopOptions choose what a Top reads and shows.
type TopOptions struct {
	Catalog         Catalog
	RootPID         int
	SkipDescriptors bool
	// All shows, and reads the disk and descriptors of, the processes in no
	// tool's group too.
	All bool
	// Groups shows only the groups of these rules, and those under them.
	Groups []string
	// Net reads each process's network bytes with nettop, every Interval.
	Net      bool
	Interval time.Duration
	// GavelCache is where gavel processes publish their status; defaults to
	// gavel's user cache directory.
	GavelCache string
}

// Top reads the host's process groups at each Tick.
type Top struct {
	mu        sync.Mutex
	sampler   *process.Sampler
	options   TopOptions
	rates     *Rates
	contexts  *contexts
	launchd   launchd
	brew      []string
	self      int
	overhead  overheadReader
	host      hostReader
	hogs      hogTracker
	net       *NetMonitor
	tick      int64
	processes []ProcessSample
	gavel     *gavelStatuses
	observed  []process.Observed
	held      map[int][]clickyprocess.Descriptor
	io        map[int]clickyprocess.Process
}

// NewTop is a Top over sampler, refusing a group no rule names; with Net it
// starts nettop, which Close stops.
func NewTop(ctx context.Context, sampler *process.Sampler, options TopOptions) (*Top, error) {
	for _, name := range options.Groups {
		if _, ok := options.Catalog.Rule(name); !ok {
			return nil, fmt.Errorf("group %q is no rule of the catalog", name)
		}
	}
	if options.GavelCache == "" {
		cache, err := status.CacheDir()
		if err != nil {
			return nil, fmt.Errorf("gavel status directory: %w", err)
		}
		options.GavelCache = cache
	}
	top := &Top{sampler: sampler, options: options, rates: NewRates(), contexts: newContexts(), brew: BrewPrefixes(), self: os.Getpid(),
		gavel: newGavelStatuses(options.GavelCache)}
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
	t.gavel.close()
}

// Tick lists the host, sorts it into groups and reads what each uses.
func (t *Top) collect(ctx context.Context) (Frame, error) {
	started := time.Now()
	listing, at, err := t.sampler.Observe(ctx, t.options.RootPID)
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
	t.observed, t.held = listing, held
	host, err := t.host.read(ctx)
	if err != nil {
		return Frame{}, err
	}
	t.hogs.observe(readings, host, at)
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
	var gavelPIDs []int
	for _, group := range classified {
		if group.Rule.Name == gavelRule {
			gavelPIDs = append(gavelPIDs, group.Anchor.PID)
		}
	}
	gavels := t.gavel.of(gavelPIDs)
	var published []status.Status
	for _, group := range classified {
		sample := GroupSample{
			SampledAt: at, Tick: t.tick, Key: group.Key, ParentKey: group.ParentKey,
			Name: group.Rule.Name, Kind: group.Rule.Kind, Target: group.Target,
			PID: group.Anchor.PID, Command: group.Anchor.Command, Context: contexts[group.Key], Origin: group.Origin,
			Self: totals[group.Key].Self, Total: totals[group.Key].Total, SkipAncestry: group.Rule.SkipAncestry,
		}
		if reading, ok := gavels[group.Anchor.PID]; ok && group.Rule.Name == gavelRule {
			sample.Gavel = reading.Status
			if reading.Err != nil {
				sample.GavelError = reading.Err.Error()
			}
			if reading.Status != nil {
				published = append(published, *reading.Status)
			}
		}
		frame.Groups = append(frame.Groups, sample)
	}
	frame.Slots = SlotPools(published)
	return frame, nil
}

// Processes are the processes of the latest Tick that filter picks, by pid.
func (t *Top) Processes(filter ProcessFilter) []ProcessSample {
	t.mu.Lock()
	defer t.mu.Unlock()
	return FilterProcesses(t.processes, filter)
}

func FilterProcesses(processes []ProcessSample, filter ProcessFilter) []ProcessSample {
	picked := map[int]bool{}
	if filter.PID != 0 {
		picked[filter.PID] = true
		// Parents may be listed after their children: grow the subtree until it holds.
		for grew := true; grew; {
			grew = false
			for _, p := range processes {
				if picked[p.PPID] && !picked[p.PID] && p.PID != p.PPID {
					picked[p.PID], grew = true, true
				}
			}
		}
	}
	var result []ProcessSample
	for _, p := range processes {
		switch {
		case filter.PID != 0 && !picked[p.PID]:
		case filter.GroupKey != "" && p.GroupKey != filter.GroupKey:
		case filter.Name != "" && p.Name != filter.Name:
		case filter.Query != "" && !p.holds(filter.Query):
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
	if len(readings) == 0 {
		t.io = nil
		return readings, nil, nil
	}
	if err := readMemory(ctx, classified, readings); err != nil {
		return nil, nil, err
	}
	pids := make([]int, 0, len(measured))
	for _, p := range measured {
		pids = append(pids, p.PID)
	}
	snapshot := clickyprocess.NewSnapshot(measured)
	if err := snapshot.PopulateIO(ctx, pids); err != nil {
		return nil, nil, err
	}
	t.io = make(map[int]clickyprocess.Process, len(pids))
	for _, p := range snapshot.All() {
		t.io[p.PID] = p
	}
	var descriptors map[int][]clickyprocess.Descriptor
	if !t.options.SkipDescriptors && len(pids) > 0 {
		var err error
		descriptors, err = clickyprocess.Descriptors(ctx, pids...)
		if err != nil {
			return nil, nil, fmt.Errorf("read sockets: %w", err)
		}
	}
	var network map[int]NetBytes // nil leaves every process's network unknown
	if t.net != nil {
		var err error
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

func keyOf(p process.Observed) string { return process.Key(p.Process) }

// CatalogFile is the groups.yaml of the first config dir that keeps one (the
// working directory's .telltale, then the global config dir), or "" when none
// does.
func CatalogFile() (string, error) {
	dirs, err := datadir.ConfigDirs()
	if err != nil {
		return "", fmt.Errorf("groups catalog: %w", err)
	}
	return datadir.ConfigFile(dirs, "groups.yaml")
}
