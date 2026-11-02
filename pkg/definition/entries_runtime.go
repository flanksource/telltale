package definition

import (
	"errors"
	"fmt"

	"github.com/flanksource/telltale/pkg/tracing/exec"
	"github.com/flanksource/telltale/pkg/tracing/goprof"
	tracehttp "github.com/flanksource/telltale/pkg/tracing/http"
	"github.com/flanksource/telltale/pkg/tracing/jfr"
	"github.com/flanksource/telltale/pkg/tracing/llm"
	"github.com/flanksource/telltale/pkg/tracing/logs"
	"github.com/flanksource/telltale/pkg/tracing/process"
	"github.com/flanksource/telltale/pkg/tracing/spans"
	tracesql "github.com/flanksource/telltale/pkg/tracing/sql"
	"github.com/flanksource/telltale/pkg/tracing/stacks"
	"github.com/flanksource/telltale/pkg/tracing/terminal"
)

// ProfileSource is where a Go profile is read from.
type ProfileSource string

func (ProfileSource) JSONSchema() map[string]any {
	return map[string]any{"type": "string", "enum": []any{goprof.SourceGops, goprof.SourcePProf}}
}

// ReadProfile is a Go profile reading which changes nothing in the process.
type ReadProfile string

// readProfiles are the profiles a read takes; mutex and block need the
// runtime's profile rate set, which only instrument.go.pprof does.
var readProfiles = []any{"heap", "cpu", "allocs", "goroutine"}

func (ReadProfile) JSONSchema() map[string]any {
	return map[string]any{"type": "string", "enum": readProfiles}
}

// MutatingProfile is a Go profile whose rate the capture sets in the runtime.
type MutatingProfile string

var mutatingProfiles = []any{"mutex", "block"}

func (MutatingProfile) JSONSchema() map[string]any {
	return map[string]any{"type": "string", "enum": mutatingProfiles}
}

func oneOf[T ~string](field string, value T, allowed []any) error {
	for _, candidate := range allowed {
		if candidate == string(value) {
			return nil
		}
	}
	return fmt.Errorf("%s %q is none of %v", field, value, allowed)
}

func profileCheckpoint(source ProfileSource, profile string, seconds int) goprof.CheckpointOptions {
	if source == "" {
		source = goprof.SourceGops
	}
	return goprof.CheckpointOptions{Source: string(source), Profile: profile, Seconds: seconds}
}

// InstrumentPProf reads the mutex or block profile of a Go process, setting
// its runtime's profile rate to do so.
type InstrumentPProf struct {
	Named
	Agent
	Source  ProfileSource   `json:"source,omitempty" clicky:"title=Source,desc=gops (default) or pprof"`
	Profile MutatingProfile `json:"profile" clicky:"required,title=Profile"`
}

func (p *InstrumentPProf) options(Located) (any, any, error) {
	if err := oneOf("profile", p.Profile, mutatingProfiles); err != nil {
		return nil, nil, err
	}
	return goprof.Options{}, profileCheckpoint(p.Source, string(p.Profile), 0), nil
}

// CollectPProf reads a heap, cpu, allocs or goroutine profile of a Go process.
type CollectPProf struct {
	Named
	Agent
	Source  ProfileSource `json:"source,omitempty" clicky:"title=Source,desc=gops (default) or pprof"`
	Profile ReadProfile   `json:"profile" clicky:"required,title=Profile"`
	Seconds int           `json:"seconds,omitempty" clicky:"title=Seconds,desc=Duration of a CPU profile window in seconds"`
}

func (p *CollectPProf) options(Located) (any, any, error) {
	if err := oneOf("profile", p.Profile, readProfiles); err != nil {
		return nil, nil, err
	}
	return goprof.Options{}, profileCheckpoint(p.Source, string(p.Profile), p.Seconds), nil
}

// Gops dumps a Go process's goroutines over its gops agent.
type Gops struct {
	Named
}

func (*Gops) options(Located) (any, any, error) {
	return stacks.Options{}, stacks.CheckpointOptions{Transport: stacks.TransportGops}, nil
}

// NativeStacks dumps the threads of any process with the OS sampler.
type NativeStacks struct {
	Named
}

func (*NativeStacks) options(Located) (any, any, error) {
	return stacks.Options{}, stacks.CheckpointOptions{Transport: stacks.TransportNative}, nil
}

// Resources samples every process of the tree.
type Resources struct {
	Named
	process.SampleOptions
}

func (r *Resources) options(Located) (any, any, error) { return r.SampleOptions, nil, nil }

// FlightRecorder starts a JFR recording in a JVM.
type FlightRecorder struct {
	Named
	Agent
	Settings      string `json:"settings,omitempty" clicky:"title=JFR settings,desc=profile (default) or default or a .jfc file"`
	Threads       bool   `json:"threads,omitempty" clicky:"title=Split by thread"`
	MaxStackDepth int    `json:"maxStackDepth,omitempty" clicky:"title=Maximum stack depth"`
}

func (f *FlightRecorder) options(Located) (any, any, error) {
	return jfr.Options{Engine: jfr.EngineJFR, Settings: f.Settings, Threads: f.Threads, MaxStackDepth: f.MaxStackDepth}, nil, nil
}

// AsyncProfiler attaches async-profiler to a JVM.
type AsyncProfiler struct {
	Named
	Agent
	Event         jfr.ProfileEvent `json:"event,omitempty" clicky:"title=Event"`
	Interval      string           `json:"interval,omitempty" clicky:"title=Sampling interval"`
	Executable    string           `json:"asyncProfiler,omitempty" clicky:"title=Async-profiler executable,desc=Local asprof executable for process captures"`
	Threads       bool             `json:"threads,omitempty" clicky:"title=Split by thread"`
	MaxStackDepth int              `json:"maxStackDepth,omitempty" clicky:"title=Maximum stack depth"`
}

func (a *AsyncProfiler) options(Located) (any, any, error) {
	return jfr.Options{
		Engine: jfr.EngineAsyncProfiler, Event: a.Event, Interval: a.Interval, AsyncProfiler: a.Executable,
		Threads: a.Threads, MaxStackDepth: a.MaxStackDepth,
	}, nil, nil
}

// HTTPProbe records the HTTP an agent's or telltale's own transports make.
type HTTPProbe struct {
	Named
	Agent
	tracehttp.Options
}

func (p *HTTPProbe) options(Located) (any, any, error) { return p.Options, nil, nil }

// SQLProbe records the statements an agent's or telltale's connections run.
type SQLProbe struct {
	Named
	Agent
	tracesql.Options
}

func (p *SQLProbe) options(Located) (any, any, error) { return p.Options, nil, nil }

// ExecProbe records the commands an agent or telltale runs. Executable is the
// exec kind's name glob, renamed so it does not take the entry's name.
type ExecProbe struct {
	Named
	Agent
	Executable string `json:"executable,omitempty" clicky:"title=Executable,desc=Executable name glob"`
	TailBytes  int    `json:"tailBytes,omitempty" clicky:"title=Tail bytes,desc=Maximum bytes of each output tail"`
}

func (p *ExecProbe) options(Located) (any, any, error) {
	return exec.Options{Name: p.Executable, TailBytes: p.TailBytes}, nil, nil
}

// LLMProbe records the provider calls an agent or telltale makes.
type LLMProbe struct {
	Named
	Agent
	llm.Options
}

func (p *LLMProbe) options(Located) (any, any, error) { return p.Options, nil, nil }

// LogsProbe records the logs an agent or telltale writes. Levels changes the
// agent's log levels, so a collect entry refuses it.
type LogsProbe struct {
	Named
	Agent
	MinLevel string            `json:"minLevel,omitempty" clicky:"title=Minimum level"`
	Match    string            `json:"match,omitempty" clicky:"title=Match"`
	Levels   map[string]string `json:"levels,omitempty" clicky:"title=Levels,desc=Refused: setting log levels changes the agent"`
}

func (p *LogsProbe) options(at Located) (any, any, error) {
	if len(p.Levels) > 0 && at.Section == SectionCollect {
		return nil, nil, errors.New("levels sets the agent's log levels, which changes its runtime: collect entries only read")
	}
	return logs.Options{MinLevel: p.MinLevel, Match: p.Match, Levels: p.Levels}, nil, nil
}

// SpansProbe records the spans an agent or telltale ends.
type SpansProbe struct {
	Named
	Agent
}

func (*SpansProbe) options(Located) (any, any, error) { return spans.Options{}, nil, nil }

// TerminalProbe records the terminal events telltale's host feeds in.
type TerminalProbe struct {
	Named
	terminal.Options
}

func (p *TerminalProbe) options(Located) (any, any, error) { return p.Options, nil, nil }
