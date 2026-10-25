package telltale

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/flanksource/telltale/pkg/traces"
)

// Status is where a run is.
type Status string

const (
	// StatusRunning is a run this process is running.
	StatusRunning Status = "running"
	// StatusSucceeded is a finished run whose command exited 0.
	StatusSucceeded Status = "succeeded"
	// StatusFailed is a finished run whose command exited non-zero or was
	// killed, or whose checkpoints failed.
	StatusFailed Status = "failed"
	// StatusUnfinished is a store with no manifest that this process is not
	// running: a run that crashed, or one another process is still running.
	StatusUnfinished Status = "unfinished"
)

// Statuses are every Status, in the order a client offers them.
var Statuses = []Status{StatusRunning, StatusSucceeded, StatusFailed, StatusUnfinished}

// ParseStatus refuses a value that names no Status.
func ParseStatus(value string) (Status, error) {
	status := Status(value)
	if !slices.Contains(Statuses, status) {
		return "", fmt.Errorf("unknown run status %q (want one of %v)", value, Statuses)
	}
	return status, nil
}

// RunInfo describes one run, live or stored.
type RunInfo struct {
	RunID     string          `json:"runId"`
	Dir       string          `json:"dir"`
	Status    Status          `json:"status"`
	Command   []string        `json:"command,omitempty"`
	PID       int             `json:"pid,omitempty"`
	StartedAt time.Time       `json:"startedAt,omitzero"`
	EndedAt   time.Time       `json:"endedAt,omitzero"`
	ExitCode  *int            `json:"exitCode,omitempty"`
	Captures  []CaptureResult `json:"captures,omitempty"`
	// Process names the process capture's streams; nil for an unfinished run
	// and a host run.
	Process *traces.ProcessRefs `json:"process,omitempty"`
	// Live is set while the run is running, with what a client checkpoints.
	Live *LiveRun `json:"live,omitempty"`
	// Unavailable says why an unfinished run has nothing to read.
	Unavailable      string   `json:"unavailable,omitempty"`
	CheckpointErrors []string `json:"checkpointErrors,omitempty"`
}

// LiveRun is what a running run offers beyond its stored streams.
type LiveRun struct {
	Sessions traces.ProcessSessions `json:"sessions"`
}

func resultInfo(result *Result) RunInfo {
	status := StatusSucceeded
	if result.ExitCode != 0 || len(result.CheckpointErrors) > 0 {
		status = StatusFailed
	}
	return RunInfo{
		RunID: result.RunID, Dir: result.Dir, Status: status, Command: result.Command, PID: result.PID, StartedAt: result.StartedAt,
		EndedAt: result.EndedAt, ExitCode: &result.ExitCode, Captures: result.Captures, Process: result.Process,
		CheckpointErrors: result.CheckpointErrors,
	}
}

// GetOptions name one run.
type GetOptions struct {
	WorkDir string
	RunID   string
}

// Get describes the run options name, live or stored; a store that is gone
// (pruned, or never recorded) is os.ErrNotExist.
func Get(options GetOptions) (RunInfo, error) {
	if options.RunID == "" || options.RunID == "." || options.RunID == ".." || strings.ContainsAny(options.RunID, `/\`) {
		return RunInfo{}, fmt.Errorf("get run: %q is not a run id", options.RunID)
	}
	dir := traces.StoreDir(options.WorkDir, options.RunID)
	run, ok, err := live.get(dir)
	if err != nil {
		return RunInfo{}, err
	}
	if ok {
		return run.Info(), nil
	}
	if _, err := os.Stat(dir); err != nil {
		return RunInfo{}, fmt.Errorf("get run %s: %w", options.RunID, err)
	}
	manifest, err := ReadManifest(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return RunInfo{RunID: options.RunID, Dir: dir, Status: StatusUnfinished, Unavailable: "the run did not finish: it left no " + ManifestFile}, nil
	case err != nil:
		return RunInfo{}, err
	}
	return resultInfo(manifest), nil
}

// ListOptions choose which runs of a work dir List returns.
type ListOptions struct {
	WorkDir string
	// Query matches runs whose id or command, or "pid N" of an attached
	// run, contains it, ignoring case.
	Query string
	// Status keeps runs in any of these statuses; empty keeps every status.
	Status []Status
	// Kind keeps runs that armed a capture of this trace kind.
	Kind string
	// Since keeps runs started at or after it; a run with no known start
	// (unfinished) is then left out.
	Since time.Time
	// Limit caps how many runs are returned; 0 returns every match.
	Limit int
}

// List describes the runs under options.WorkDir that match options, newest
// first. A work dir that has never traced a run has none.
func List(options ListOptions) ([]RunInfo, error) {
	entries, err := os.ReadDir(filepath.Join(options.WorkDir, traces.Dir))
	if errors.Is(err, os.ErrNotExist) {
		return []RunInfo{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	runs := []RunInfo{}
	for _, entry := range slices.Backward(entries) {
		if !entry.IsDir() {
			continue
		}
		info, err := Get(GetOptions{WorkDir: options.WorkDir, RunID: entry.Name()})
		if errors.Is(err, os.ErrNotExist) {
			continue // pruned since it was listed
		}
		if err != nil {
			return nil, err
		}
		if !options.matches(info) {
			continue
		}
		if runs = append(runs, info); options.Limit > 0 && len(runs) == options.Limit {
			break
		}
	}
	return runs, nil
}

func (o ListOptions) matches(info RunInfo) bool {
	if query := strings.ToLower(o.Query); query != "" &&
		!strings.Contains(strings.ToLower(info.RunID), query) &&
		!strings.Contains(strings.ToLower(strings.Join(info.Command, " ")), query) &&
		(info.PID == 0 || !strings.Contains("pid "+strconv.Itoa(info.PID), query)) {
		return false
	}
	if len(o.Status) > 0 && !slices.Contains(o.Status, info.Status) {
		return false
	}
	if o.Kind != "" && !slices.ContainsFunc(info.Captures, func(c CaptureResult) bool { return c.Kind == o.Kind }) {
		return false
	}
	return o.Since.IsZero() || (!info.StartedAt.IsZero() && !info.StartedAt.Before(o.Since))
}
