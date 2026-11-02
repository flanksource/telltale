package traces

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/process"
	"github.com/flanksource/telltale/pkg/tracing/stacks"
	"github.com/flanksource/telltale/pkg/tracing/target"
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
	// Tree is what the trace reads of each process beside its samples.
	Tree TreeOptions
}

// TreeOptions choose what the run-wide process tree reads of each process:
// the same environment params a process_tree capture takes, and the facets.
type TreeOptions struct {
	// Facets arms every process_samples facet and, unless Environment narrows
	// it, reads each process's whole environment onto its node, as a run
	// watched in a UI does.
	Facets bool `json:"facets,omitempty" clicky:"title=Process facets,desc=Read every process's open files sockets and environment into the process tree"`
	// Environment reads only these keys and prefixes of each process's
	// environment onto its node, with or without Facets; empty ones read all
	// of it. Absent reads none, unless Facets is set.
	Environment *process.EnvironmentParams `json:"environment,omitempty" clicky:"title=Environment,desc=Environment variables read onto each process"`
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
	root, interval := target.Target{PID: options.RootPID}, process.Options{Interval: options.Interval.String()}
	tree := process.TreeParams{TreeOptions: process.TreeOptions{Options: interval}, Target: root}
	samples := process.SampleParams{SampleOptions: process.SampleOptions{Options: interval}, Target: root}
	if options.Tree.Facets {
		tree.Environment, samples.Facets = &process.EnvironmentParams{}, process.Facets
	}
	if options.Tree.Environment != nil {
		tree.Environment = options.Tree.Environment
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
		{&trace.stacks, stacks.Kind, stacks.Params{Target: target.Target{PID: options.RootPID}}},
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
	return h.Start(StartOptions{Kind: kind, Params: raw})
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
// latest tick. A tree whose root exited ended by itself with that last
// observation committed, so there is nothing left to re-observe.
func (p *ProcessTrace) CheckpointTree(ctx context.Context) error {
	err := p.host.runtime.Checkpoint(ctx, p.tree.managed)
	if session := p.tree.managed.Session(); errors.Is(err, query.ErrSessionEnded) && session.Snapshot().Error == "" {
		return nil
	}
	if err != nil {
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
