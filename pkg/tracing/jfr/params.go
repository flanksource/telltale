package jfr

import (
	"github.com/flanksource/telltale/pkg/tracing/target"
	"time"
)

type Params struct {
	Target        target.Target `json:"target,omitzero"`
	Engine        Engine        `json:"engine" clicky:"enum=jfr,async-profiler,title=Engine"`
	Settings      string        `json:"settings,omitempty" clicky:"title=JFR settings"`
	Event         ProfileEvent  `json:"event,omitempty" clicky:"title=Async-profiler event"`
	Interval      string        `json:"interval,omitempty" clicky:"title=Sampling interval"`
	Threads       bool          `json:"threads,omitempty" clicky:"title=Split by thread,desc=Retain thread identities in decoded samples"`
	MaxStackDepth int           `json:"maxStackDepth,omitempty" clicky:"title=Maximum stack depth,desc=Keep this many leaf frames; zero keeps all recorded frames"`
	AsyncProfiler string        `json:"asyncProfiler,omitempty" clicky:"title=Async-profiler executable,desc=Local asprof executable for process captures"`
}

type ProfileEvent string

func (ProfileEvent) JSONSchema() map[string]any {
	return map[string]any{"type": "string", "enum": asyncProfilerEvents}
}

func (p Params) JFROptions() (Options, error) {
	o := Options{Engine: p.Engine, Settings: p.Settings, Event: string(p.Event), Threads: p.Threads, MaxStackDepth: p.MaxStackDepth, PID: p.Target.PID, AsyncProfiler: p.AsyncProfiler}
	if o.Engine == EngineJFR && o.Settings == "" {
		o.Settings = "profile"
	}
	if p.Interval != "" {
		parsed, err := time.ParseDuration(p.Interval)
		if err != nil {
			return o, err
		}
		o.Interval = parsed
	}
	return o, o.Validate()
}
func (p Params) Validate() error { _, err := p.JFROptions(); return err }
