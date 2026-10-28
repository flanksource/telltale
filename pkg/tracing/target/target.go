// Package target describes where a trace kind is armed.
package target

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// ParseSelector parses comma-separated attribute equality predicates.
func ParseSelector(value string) (map[string]string, error) {
	if value == "" {
		return nil, fmt.Errorf("%w: selector must contain key=value pairs", ErrInvalidTarget)
	}
	selector := make(map[string]string)
	for _, pair := range strings.Split(value, ",") {
		key, val, ok := strings.Cut(pair, "=")
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		if !ok || key == "" || val == "" {
			return nil, fmt.Errorf("%w: selector %q must be key=value", ErrInvalidTarget, pair)
		}
		if _, exists := selector[key]; exists {
			return nil, fmt.Errorf("%w: selector key %q is repeated", ErrInvalidTarget, key)
		}
		selector[key] = val
	}
	return selector, nil
}

// Seam identifies how a trace is armed.
type Seam string

const (
	Self    Seam = "self"
	Agent   Seam = "agent"
	Process Seam = "process"
	Proxy   Seam = "proxy"
	Host    Seam = "host"
)

var (
	ErrUnsupportedSeam = errors.New("unsupported seam")
	ErrNoPIDSeam       = errors.New("no seam available for pid")
	ErrInvalidTarget   = errors.New("invalid target")
	ErrNoMatchingAgent = errors.New("no matching agent")
)

// Target selects a seam and its process or agent.
type Target struct {
	Seam          Seam              `json:"seam,omitzero" clicky:"title=Seam,desc=How the capture is armed"`
	PID           int               `json:"pid,omitzero" clicky:"title=PID,desc=Process to observe"`
	Agent         string            `json:"agent,omitzero" clicky:"title=Agent,desc=Agent ID to observe"`
	Selector      map[string]string `json:"selector,omitzero" clicky:"title=Selector,desc=Agent resource attributes to match"`
	Wait          bool              `json:"wait,omitzero" clicky:"title=Wait for agents,desc=Wait for a matching agent to connect"`
	AllowMutation bool              `json:"allowMutation,omitzero" clicky:"title=Allow mutation,desc=Allow probes to change runtime settings"`
}

// Resolve chooses a supported seam for a target using the available agent lookup.
func Resolve(probe string, t Target, seams []Seam, defaultSeam Seam, available func(Target) bool) (Target, error) {
	if t.PID < 0 || (t.Agent != "" && len(t.Selector) > 0) || (t.PID != 0 && (t.Agent != "" || len(t.Selector) > 0)) {
		return Target{}, fmt.Errorf("%w: choose one of pid, agent or selector", ErrInvalidTarget)
	}
	if t.Wait && len(t.Selector) == 0 {
		return Target{}, fmt.Errorf("%w: wait requires a selector", ErrInvalidTarget)
	}
	for key, value := range t.Selector {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			return Target{}, fmt.Errorf("%w: selector keys and values must not be blank", ErrInvalidTarget)
		}
	}
	if t.Seam != "" && !slices.Contains(seams, t.Seam) {
		return Target{}, fmt.Errorf("%w %q; supported seams: %v", ErrUnsupportedSeam, t.Seam, seams)
	}
	if t.Seam == "" {
		switch {
		case t.Agent != "" || len(t.Selector) > 0:
			t.Seam = Agent
		case t.PID != 0 && slices.Contains(seams, Agent) && available != nil && available(t):
			t.Seam = Agent
		case t.PID != 0 && slices.Contains(seams, Process):
			t.Seam = Process
		case t.PID != 0:
			return Target{}, fmt.Errorf("%w: no agent advertises probe %s for pid %d; kind supports seams %v", ErrNoPIDSeam, probe, t.PID, seams)
		default:
			t.Seam = defaultSeam
		}
	}
	if !slices.Contains(seams, t.Seam) {
		return Target{}, fmt.Errorf("%w %q; supported seams: %v", ErrUnsupportedSeam, t.Seam, seams)
	}
	if ((t.Agent != "" || len(t.Selector) > 0) && t.Seam != Agent) || (t.PID != 0 && t.Seam != Agent && t.Seam != Process) {
		return Target{}, fmt.Errorf("%w: seam %q requires a compatible pid, agent or selector; supported seams: %v", ErrInvalidTarget, t.Seam, seams)
	}
	if t.Seam == Agent && t.PID == 0 && t.Agent == "" && len(t.Selector) == 0 {
		return Target{}, fmt.Errorf("%w: agent seam requires a pid, agent or selector; supported seams: %v", ErrInvalidTarget, seams)
	}
	if t.Seam == Agent && len(t.Selector) > 0 && !t.Wait && (available == nil || !available(t)) {
		return Target{}, fmt.Errorf("%w for selector %v; use --wait or choose an agent", ErrNoMatchingAgent, t.Selector)
	}
	if t.Seam == Agent && (t.PID != 0 || t.Agent != "") && (available == nil || !available(t)) {
		return Target{}, fmt.Errorf("%w for pid %d or agent %q; supported seams: %v", ErrNoMatchingAgent, t.PID, t.Agent, seams)
	}
	return t, nil
}
