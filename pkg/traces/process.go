package traces

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/process"
	"github.com/flanksource/telltale/pkg/tracing/stacks"
)

// ProcessTrace is the run-scope capture of the process kinds over one root:
// its tree, its samples and its stacks, the streams the Processes tab reads
// through the query mount.
type ProcessTrace struct {
	host     *Host
	interval time.Duration
	tree     *Capture
	samples  *Capture
	stacks   *Capture
}

// ProcessTraceOptions choose what a process trace reads.
type ProcessTraceOptions struct {
	// RootPID is the process whose tree is traced.
	RootPID int
	// Interval is how often the tree is sampled; zero is a second.
	Interval time.Duration
	// Facets arms every process_samples facet and reads each process's whole
	// environment onto its node, as a run watched in a UI does.
	Facets bool
}

// ProcessRefs name the process capture's streams in the run's store.
type ProcessRefs struct {
	Tree    tracing.TraceRef `json:"tree"`
	Samples tracing.TraceRef `json:"samples"`
	Stacks  tracing.TraceRef `json:"stacks"`
}

// ProcessSessions are the running process capture's sessions, which a client
// checkpoints through the sessions API: stacks to dump a process's stacks,
// samples to read a process's facet lists.
type ProcessSessions struct {
	Tree    string `json:"tree"`
	Samples string `json:"samples"`
	Stacks  string `json:"stacks"`
}

// StartProcessTrace captures the process tree under options.RootPID, its
// samples and, at each checkpoint, its stacks.
func (h *Host) StartProcessTrace(options ProcessTraceOptions) (*ProcessTrace, error) {
	if options.Interval == 0 {
		options.Interval = time.Second
	}
	base := process.Params{RootPID: options.RootPID, Interval: options.Interval.String()}
	tree, samples := process.TreeParams{Params: base}, process.SampleParams{Params: base}
	if options.Facets {
		tree.Environment, samples.Facets = &process.EnvironmentParams{}, process.Facets
	}
	trace := &ProcessTrace{host: h, interval: options.Interval}
	var err error
	for _, start := range []struct {
		capture **Capture
		kind    string
		params  any
	}{
		{&trace.tree, process.TreeKind, tree},
		{&trace.samples, process.SamplesKind, samples},
		{&trace.stacks, stacks.Kind, stacks.Params{RootPID: options.RootPID}},
	} {
		if *start.capture, err = h.startWith(start.kind, start.params); err != nil {
			return nil, errors.Join(err, trace.Stop(context.Background()))
		}
	}
	return trace, nil
}

func (h *Host) startWith(kind string, params any) (*Capture, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("encode %s params: %w", kind, err)
	}
	return h.Start(kind, raw)
}

// Interval is how often the trace samples the tree.
func (p *ProcessTrace) Interval() time.Duration { return p.interval }

// Refs are the whole tree, samples and stacks streams.
func (p *ProcessTrace) Refs() ProcessRefs {
	return ProcessRefs{Tree: p.tree.Ref(0, 0), Samples: p.samples.Ref(0, 0), Stacks: p.stacks.Ref(0, 0)}
}

// Sessions are the captures' sessions.
func (p *ProcessTrace) Sessions() ProcessSessions {
	return ProcessSessions{
		Tree: p.tree.managed.Session().ID(), Samples: p.samples.managed.Session().ID(), Stacks: p.stacks.managed.Session().ID(),
	}
}

// CheckpointTree re-observes the tree, committing every change since its
// latest tick.
func (p *ProcessTrace) CheckpointTree(ctx context.Context) error {
	if err := p.host.runtime.Checkpoint(ctx, p.tree.managed); err != nil {
		return fmt.Errorf("checkpoint the process tree: %w", err)
	}
	return nil
}

// CheckpointStacks dumps the stacks params name, or records the dump they
// carry, committed when it returns.
func (p *ProcessTrace) CheckpointStacks(ctx context.Context, params stacks.CheckpointParams) error {
	if err := p.stacks.Checkpoint(ctx, params); err != nil {
		return fmt.Errorf("%s: %w", params.Reason, err)
	}
	return nil
}

// Stop ends the captures; their streams stay in the run's store.
func (p *ProcessTrace) Stop(ctx context.Context) error {
	var errs []error
	for _, capture := range []*Capture{p.tree, p.samples, p.stacks} {
		if capture != nil {
			_, err := capture.Stop(ctx)
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
