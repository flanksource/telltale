package jfr

import (
	"github.com/flanksource/telltale/pkg/tracing/target"
	"time"
)

// Options choose the engine a JVM capture records with and what it keeps of
// the recording.
type Options struct {
	Engine        Engine       `json:"engine" clicky:"enum=jfr,async-profiler,title=Engine"`
	Settings      string       `json:"settings,omitempty" clicky:"title=JFR settings"`
	Event         ProfileEvent `json:"event,omitempty" clicky:"title=Async-profiler event"`
	Interval      string       `json:"interval,omitempty" clicky:"title=Sampling interval"`
	Threads       bool         `json:"threads,omitempty" clicky:"title=Split by thread,desc=Retain thread identities in decoded samples"`
	MaxStackDepth int          `json:"maxStackDepth,omitempty" clicky:"title=Maximum stack depth,desc=Keep this many leaf frames; zero keeps all recorded frames"`
	AsyncProfiler string       `json:"asyncProfiler,omitempty" clicky:"title=Async-profiler executable,desc=Local asprof executable for process captures"`
}

// Params are a capture's Options on the JVM its target names.
type Params struct {
	Target target.Target `json:"target,omitzero"`
	Options
}

type ProfileEvent string

func (ProfileEvent) JSONSchema() map[string]any {
	return map[string]any{"type": "string", "enum": asyncProfilerEvents}
}

func (p Params) JFROptions() (RecordOptions, error) { return p.record(p.Target.PID) }

func (p Params) Validate() error { _, err := p.JFROptions(); return err }

// Validate is the static check of the engine and its options: it attaches to
// no JVM and looks up no executable.
func (o Options) Validate() error { _, err := o.record(0); return err }

// record is what a capture of pid records with o.
func (o Options) record(pid int) (RecordOptions, error) {
	r := RecordOptions{Engine: o.Engine, Settings: o.Settings, Event: string(o.Event), Threads: o.Threads, MaxStackDepth: o.MaxStackDepth, PID: pid, AsyncProfiler: o.AsyncProfiler}
	if r.Engine == EngineJFR && r.Settings == "" {
		r.Settings = "profile"
	}
	if o.Interval != "" {
		parsed, err := time.ParseDuration(o.Interval)
		if err != nil {
			return r, err
		}
		r.Interval = parsed
	}
	return r, r.Validate()
}
