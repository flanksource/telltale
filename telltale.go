// Package telltale runs commands under trace and finds the runs it recorded:
// Start opens a run's store at .telltale/traces/<run id>, arms the captures
// asked for, points the command at them through their exports and records the
// process tree it spawns; Stop ends a live run; List and Get read the runs a
// work dir holds, live or stored.
package telltale

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/flanksource/commons-db/connection/store"
	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/tracing"
)

// TraceSpec is one capture to arm: a trace kind and its params.
type TraceSpec struct {
	Kind   string
	Params json.RawMessage
	// Checkpoint are the checkpoint params the run reads the capture with;
	// nil is its kind's registered ones (traces.Registration.Checkpoint).
	Checkpoint json.RawMessage
}

// ParseTraceSpec reads kind or kind:{json params}.
func ParseTraceSpec(value string) (TraceSpec, error) {
	kind, params, _ := strings.Cut(value, ":")
	if strings.TrimSpace(kind) == "" {
		return TraceSpec{}, fmt.Errorf("trace %q names no kind; want kind or kind:{json}", value)
	}
	spec := TraceSpec{Kind: strings.TrimSpace(kind)}
	if params != "" {
		if !json.Valid([]byte(params)) {
			return TraceSpec{}, fmt.Errorf("trace %s params are not JSON: %s", spec.Kind, params)
		}
		spec.Params = json.RawMessage(params)
	}
	return spec, nil
}

// TraceRoot is the process whose tree a run records.
type TraceRoot int

const (
	// TraceRootSelf records the tree under this process, the command among
	// it, as a CLI run in the foreground does.
	TraceRootSelf TraceRoot = iota
	// TraceRootCommand records the tree under the command alone, started in
	// its own process group, as a long-lived host starting runs does.
	TraceRootCommand
)

// StartOptions say what Start runs and traces.
type StartOptions struct {
	// WorkDir holds the run stores, under traces.Dir.
	WorkDir string
	// Dir is the directory the command runs in; empty is this process's.
	Dir string
	// Command is the program and its arguments. A run has a Command or a
	// PID, not both; a run with neither is a host run, which arms only
	// host captures and runs until stopped or Duration passes.
	Command []string
	// PID attaches the run to a running process instead of starting one: its
	// tree is traced and every process capture is rooted at it,
	// until the run is stopped, Duration passes or the process exits.
	PID int
	// Duration stops the run once it has run this long; zero runs until the
	// command exits or the run is stopped.
	Duration time.Duration
	// Every is how often each checkpointed capture is read (its kind's
	// traces.Registration.Checkpoint); zero reads an attached run's once, as
	// it stops, and a command's never.
	Every time.Duration
	// Traces are the captures armed around the command.
	Traces []TraceSpec
	// Facets reads every process's facets and environment into the tree.
	Facets    bool
	TraceRoot TraceRoot
	Stdin     io.Reader
	// Stdout and Stderr take the command's output; when both are nil it is
	// written to OutputFile in the run's store.
	Stdout, Stderr io.Writer
	// Connections answer the connection references the captures name, such
	// as sql_xevent's connection; nil answers none (OpenConnections is
	// telltale's own).
	Connections *store.Store
}

// Result is what a finished run recorded, kept in its store as ManifestFile.
type Result struct {
	RunID     string          `json:"runId"`
	Dir       string          `json:"dir"`
	Command   []string        `json:"command,omitempty"`
	PID       int             `json:"pid,omitempty"`
	StartedAt time.Time       `json:"startedAt"`
	EndedAt   time.Time       `json:"endedAt"`
	ExitCode  int             `json:"exitCode"`
	Captures  []CaptureResult `json:"captures"`
	// Process names the process trace's streams; nil for a host run.
	Process *traces.ProcessRefs `json:"process,omitempty"`
	// CheckpointErrors are the checkpoints that failed, such as a dump of a
	// process that had exited; a run with any failed.
	CheckpointErrors []string `json:"checkpointErrors,omitempty"`
}

// CaptureResult is one capture's stream and, once the run finished, its
// kind's summary of it.
type CaptureResult struct {
	Kind    string           `json:"kind"`
	Ref     tracing.TraceRef `json:"ref"`
	Summary map[string]any   `json:"summary,omitempty"`
}

// ManifestFile is the file a finished run leaves its Result in, in its store.
const ManifestFile = "run.json"

// OutputFile is where a run started without Stdout and Stderr writes the
// command's output, in its store.
const OutputFile = "output.log"

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
