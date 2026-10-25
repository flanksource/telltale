// Package tracerun runs a command under trace: it opens a run's store at
// .telltale/traces/<runID>, arms the captures asked for, points the command at
// them through their exports, and records the process tree it spawns.
package tracerun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/stacks"
)

// Spec is one capture to arm: a trace kind and its params.
type Spec struct {
	Kind   string
	Params json.RawMessage
}

// ParseSpec reads kind or kind:{json params}.
func ParseSpec(value string) (Spec, error) {
	kind, params, _ := strings.Cut(value, ":")
	if strings.TrimSpace(kind) == "" {
		return Spec{}, fmt.Errorf("trace %q names no kind; want kind or kind:{json}", value)
	}
	spec := Spec{Kind: strings.TrimSpace(kind)}
	if params != "" {
		if !json.Valid([]byte(params)) {
			return Spec{}, fmt.Errorf("trace %s params are not JSON: %s", spec.Kind, params)
		}
		spec.Params = json.RawMessage(params)
	}
	return spec, nil
}

// Options say what Run runs and traces.
type Options struct {
	// WorkDir holds the run stores, under traces.Dir.
	WorkDir string
	// Command is the program and its arguments.
	Command []string
	// Traces are the captures armed around the command.
	Traces []Spec
	// Facets reads every process's facets and environment into the tree.
	Facets         bool
	Stdin          io.Reader
	Stdout, Stderr io.Writer
}

// Result is what a run recorded.
type Result struct {
	RunID    string             `json:"runId"`
	Dir      string             `json:"dir"`
	Command  []string           `json:"command"`
	ExitCode int                `json:"exitCode"`
	Captures []CaptureResult    `json:"captures"`
	Process  traces.ProcessRefs `json:"process"`
}

// CaptureResult is one capture's stream and its kind's summary of it.
type CaptureResult struct {
	Kind    string           `json:"kind"`
	Ref     tracing.TraceRef `json:"ref"`
	Summary map[string]any   `json:"summary"`
}

// Run runs options.Command under trace and returns what it recorded; a
// command that exits non-zero is reported in Result.ExitCode, not as an error.
func Run(ctx context.Context, options Options) (*Result, error) {
	if len(options.Command) == 0 {
		return nil, errors.New("trace run: a command is required")
	}
	host, err := traces.Open(options.WorkDir, traces.RunID(time.Now()))
	if err != nil {
		return nil, err
	}
	run := &tracedRun{host: host}
	result, err := run.execute(ctx, options)
	if err = errors.Join(err, run.close(options.WorkDir)); err != nil {
		return nil, err
	}
	return result, writeManifest(result)
}

// ManifestFile is the file a finished run leaves its Result in, in its store.
const ManifestFile = "run.json"

func writeManifest(result *Result) error {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("encode run manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(result.Dir, ManifestFile), data, 0o644); err != nil {
		return fmt.Errorf("write run manifest: %w", err)
	}
	return nil
}

// ReadManifest is the Result the run that kept its store in dir finished with.
func ReadManifest(dir string) (*Result, error) {
	data, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if err != nil {
		return nil, fmt.Errorf("read run manifest: %w", err)
	}
	var result Result
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("decode run manifest %s: %w", filepath.Join(dir, ManifestFile), err)
	}
	return &result, nil
}

type tracedRun struct {
	host     *traces.Host
	process  *traces.ProcessTrace
	captures []*traces.Capture
}

func (r *tracedRun) execute(ctx context.Context, options Options) (*Result, error) {
	env, err := r.arm(options.Traces)
	if err != nil {
		return nil, err
	}
	if r.process, err = r.host.StartProcessTrace(traces.ProcessTraceOptions{RootPID: os.Getpid(), Facets: options.Facets}); err != nil {
		return nil, fmt.Errorf("process trace: %w", err)
	}
	exitCode, err := runCommand(ctx, options, env)
	if err != nil {
		return nil, err
	}
	result := &Result{RunID: r.host.RunID(), Dir: r.host.Dir(), Command: options.Command, ExitCode: exitCode, Process: r.process.Refs()}
	if result.Captures, err = r.summarize(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

// arm starts every capture and returns the environment they export, refusing
// two captures that export one variable differently.
func (r *tracedRun) arm(specs []Spec) (map[string]string, error) {
	env := map[string]string{}
	for _, spec := range specs {
		capture, err := r.host.Start(spec.Kind, spec.Params)
		if err != nil {
			return nil, err
		}
		r.captures = append(r.captures, capture)
		for name, value := range capture.Exports() {
			if previous, ok := env[name]; ok && previous != value {
				return nil, fmt.Errorf("traces %s and an earlier one both export %s", spec.Kind, name)
			}
			env[name] = value
		}
	}
	return env, nil
}

func runCommand(ctx context.Context, options Options, env map[string]string) (int, error) {
	cmd := exec.CommandContext(ctx, options.Command[0], options.Command[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = options.Stdin, options.Stdout, options.Stderr
	cmd.Env = os.Environ()
	for _, name := range slices.Sorted(maps.Keys(env)) {
		cmd.Env = append(cmd.Env, name+"="+env[name])
	}
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	if err != nil {
		return 0, fmt.Errorf("run %s: %w", options.Command[0], err)
	}
	return 0, nil
}

// summarize stops every capture, committing its stream, and summarizes it.
func (r *tracedRun) summarize(ctx context.Context) ([]CaptureResult, error) {
	results := make([]CaptureResult, 0, len(r.captures))
	for _, capture := range r.captures {
		seq, err := capture.Stop(ctx)
		if err != nil {
			return nil, err
		}
		ref := capture.Ref(0, seq)
		summary, err := r.host.Summarize(ctx, ref)
		if err != nil {
			return nil, fmt.Errorf("summarize %s trace: %w", capture.Kind(), err)
		}
		results = append(results, CaptureResult{Kind: capture.Kind(), Ref: ref, Summary: summary})
	}
	return results, nil
}

// close takes a last reading of the process capture — telltale's final
// goroutines into its stacks, a re-observed tree — stops the run's captures
// and closes its store, which stays on disk to be served; the oldest runs'
// stores beyond traces.KeepRuns go.
func (r *tracedRun) close(workDir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var processErr error
	if r.process != nil {
		processErr = errors.Join(
			r.process.CheckpointStacks(ctx, stacks.CheckpointParams{PID: os.Getpid(), Transport: stacks.TransportInProcess, Reason: "final"}),
			r.process.CheckpointTree(ctx),
			r.process.Stop(ctx),
		)
	}
	return errors.Join(processErr, r.host.Close(ctx), traces.Prune(workDir, traces.KeepRuns))
}
