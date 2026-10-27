package clicky

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"
	"unicode/utf8"

	"github.com/flanksource/clicky/exec"
	telltale "github.com/flanksource/telltale/sdk"
	"github.com/flanksource/telltale/sdk/protocol"
)

type Record struct {
	Name       string    `json:"name"`
	Args       []string  `json:"args,omitempty"`
	Dir        string    `json:"dir,omitempty"`
	EnvKeys    []string  `json:"envKeys,omitempty"`
	PID        int       `json:"pid"`
	StartedAt  time.Time `json:"startedAt"`
	DurationMs float64   `json:"durationMs"`
	ExitCode   int       `json:"exitCode"`
	Error      string    `json:"error,omitempty"`
	StdoutTail string    `json:"stdoutTail,omitempty"`
	StderrTail string    `json:"stderrTail,omitempty"`
}

type params struct {
	Name      string `json:"name"`
	TailBytes int    `json:"tailBytes"`
}
type probe struct{}

func init()                            { telltale.RegisterProbe(probe{}) }
func (probe) Spec() protocol.ProbeSpec { return protocol.ProbeSpec{Name: "exec", Title: "Exec"} }
func (probe) Arm(_ context.Context, raw json.RawMessage, sink telltale.Sink) (func() error, error) {
	var p params
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("exec params: %w", err)
	}
	if p.Name == "" {
		p.Name = "*"
	}
	if _, err := filepath.Match(p.Name, ""); err != nil {
		return nil, fmt.Errorf("exec name glob: %w", err)
	}
	if p.TailBytes < 0 || p.TailBytes > exec.TailBytes {
		return nil, fmt.Errorf("exec tailBytes must be between 0 and %d", exec.TailBytes)
	}
	release := exec.ObserveExec(func(event exec.ExecEvent) {
		match, _ := filepath.Match(p.Name, filepath.Base(event.Name))
		if event.Phase == exec.ExecExit && match {
			sink.TryEmit(makeRecord(event, p.TailBytes))
		}
	})
	return func() error { release(); return nil }, nil
}

func makeRecord(event exec.ExecEvent, limit int) Record {
	args := telltale.MaskArgs(append([]string{event.Name}, event.Args...))
	r := Record{Name: event.Name, Args: args[1:], Dir: event.Dir, EnvKeys: event.EnvKeys, PID: event.PID, StartedAt: event.StartedAt, DurationMs: float64(event.Duration) / float64(time.Millisecond), ExitCode: event.ExitCode, StdoutTail: tail(telltale.Mask(event.Stdout), limit), StderrTail: tail(telltale.Mask(event.Stderr), limit)}
	if event.Err != nil {
		r.Error = telltale.Mask(event.Err.Error())
	}
	return r
}
func tail(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	start := len(value) - limit
	for start < len(value) && !utf8.RuneStart(value[start]) {
		start++
	}
	return value[start:]
}
