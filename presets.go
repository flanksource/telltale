package telltale

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/tracing/goprof"
	"github.com/flanksource/telltale/pkg/tracing/stacks"
)

// Preset is a named run: the captures it arms and how often it reads them,
// aimed at a process to attach to or at a command. The CLI generates a
// `telltale <name>` command from each registered preset.
type Preset struct {
	Name, Summary, Description string
	Examples                   []traces.Example
	Traces                     []TraceSpec
	// Facets reads every process's facets and environment into the run's
	// process tree.
	Facets bool
	// Every is how often the checkpointed captures are read, unless the run
	// asks for another interval.
	Every time.Duration
	// Readable refuses a process to attach to that the preset cannot read,
	// before the run starts; nil reads any.
	Readable func(pid int) error
}

// Start starts the preset's run: options with its captures, facets and
// interval, refusing a process it cannot read.
func (p Preset) Start(ctx context.Context, options StartOptions) (*Run, error) {
	if p.Readable != nil && options.PID != 0 {
		if err := p.Readable(options.PID); err != nil {
			return nil, fmt.Errorf("%s: %w", p.Name, err)
		}
	}
	options.Traces = slices.Clone(p.Traces)
	options.Facets = options.Facets || p.Facets
	if options.Every == 0 {
		options.Every = p.Every
	}
	return Start(ctx, options)
}

func (p Preset) validate() error {
	var errs []error
	if strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.Summary) == "" {
		errs = append(errs, errors.New("a name and a summary are required"))
	}
	if _, err := traces.Registered(p.Name); err == nil {
		errs = append(errs, fmt.Errorf("%q is a trace kind's name", p.Name))
	}
	for _, spec := range p.Traces {
		if _, err := traces.Registered(spec.Kind); err != nil {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("register preset %q: %w", p.Name, err)
	}
	return nil
}

var presets = struct {
	mu     sync.RWMutex
	byName map[string]Preset
}{byName: map[string]Preset{}}

// RegisterPreset adds a preset. Register it in an init func, so it is
// registered before the CLI is generated from the presets.
func RegisterPreset(preset Preset) error {
	if err := preset.validate(); err != nil {
		return err
	}
	presets.mu.Lock()
	defer presets.mu.Unlock()
	if _, ok := presets.byName[preset.Name]; ok {
		return fmt.Errorf("register preset %q: it is registered twice", preset.Name)
	}
	presets.byName[preset.Name] = preset
	return nil
}

// MustRegisterPreset is RegisterPreset for an init func, panicking on a
// preset it refuses.
func MustRegisterPreset(preset Preset) {
	if err := RegisterPreset(preset); err != nil {
		panic(err)
	}
}

// Presets are the registered presets, sorted by name.
func Presets() []Preset {
	presets.mu.RLock()
	defer presets.mu.RUnlock()
	all := make([]Preset, 0, len(presets.byName))
	for _, preset := range presets.byName {
		all = append(all, preset)
	}
	slices.SortFunc(all, func(a, b Preset) int { return cmp.Compare(a.Name, b.Name) })
	return all
}

// gopsCheckpointEvery matches a gops agent's CPU profile, which runs 30s.
const gopsCheckpointEvery = 30 * time.Second

func init() {
	gopsStacks, err := json.Marshal(stacks.CheckpointParams{Transport: stacks.TransportGops, Reason: "interval"})
	if err != nil {
		panic(err)
	}
	MustRegisterPreset(Preset{
		Name: "ps", Summary: "Follow a process tree: its processes, their resources, descriptors and environment",
		Description: "Records the tree under a running process, or under a command, as it changes: every process as it starts and exits, " +
			"with its CPU, memory and disk IO each second, its open files, sockets, ports and connections, and its environment. " +
			"It runs until Ctrl+C, --duration, or the process exits.",
		Facets: true,
		Examples: []traces.Example{
			{Args: "1234 --duration 30s", Says: "follow process 1234's tree for 30 seconds"},
			{Args: "-- make build", Says: "follow the build's tree until it exits"},
		},
	})
	MustRegisterPreset(Preset{
		Name: "gops", Summary: "Read the goroutines and heap of a Go process over its gops agent",
		Description: "Dumps the goroutines of a Go process that runs a gops agent (github.com/google/gops/agent), and reads its heap profile, " +
			"every 30s and as an attached run stops. The agent is found from the port file it writes; a process without one is refused.",
		Traces: []TraceSpec{{Kind: stacks.Kind, Checkpoint: gopsStacks}, {Kind: goprof.Kind}},
		Every:  gopsCheckpointEvery,
		Readable: func(pid int) error {
			_, err := stacks.GopsPort(pid)
			return err
		},
		Examples: []traces.Example{
			{Args: "1234", Says: "read process 1234 every 30 seconds until Ctrl+C"},
			{Args: "1234 --every 5s --duration 1m", Says: "read it every 5 seconds for a minute"},
		},
	})
}
