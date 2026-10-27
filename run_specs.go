package telltale

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/tracing/seam"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

// aimedSpec is one capture a run arms: its spec, aimed at the run's target,
// and the checkpoint params the run reads it with, nil for none.
type aimedSpec struct {
	TraceSpec
	target     target.Seam
	checkpoint json.RawMessage
}

// validate refuses options that name two targets, a process that is not
// running, and a capture that cannot observe the target; it opens nothing. A
// run with neither a command nor a pid is a host run (StartOptions.host),
// which aimedSpecs refuses unless every capture it arms observes the host.
func (o StartOptions) validate() error {
	switch {
	case o.host() && len(o.Traces) == 0:
		return errors.New("start trace run: a command, a pid to attach to, or a host capture is required")
	case len(o.Command) > 0 && o.PID != 0:
		return fmt.Errorf("start trace run: run a command or attach to pid %d, not both", o.PID)
	case o.PID < 0:
		return fmt.Errorf("start trace run: pid %d is not a process id", o.PID)
	case o.Duration < 0 || o.Every < 0:
		return fmt.Errorf("start trace run: duration %s and every %s must not be negative", o.Duration, o.Every)
	case o.PID != 0 && !alive(o.PID):
		return fmt.Errorf("start trace run: pid %d is not running", o.PID)
	}
	return nil
}

// host reports a run of neither a command nor a process: it records only
// host captures, until it is stopped or Duration passes.
func (o StartOptions) host() bool { return len(o.Command) == 0 && o.PID == 0 }

// aimedSpecs aims each of options.Traces at the run's target: an attached
// run roots each process capture at its pid, unless its params name a
// root, and refuses proxy captures; a host run accepts only host captures.
func aimedSpecs(options StartOptions) ([]aimedSpec, error) {
	specs := make([]aimedSpec, 0, len(options.Traces))
	for _, spec := range options.Traces {
		registration, err := traces.Registered(spec.Kind)
		if err != nil {
			return nil, err
		}
		var selection struct {
			Target target.Target `json:"target"`
		}
		if len(spec.Params) > 0 {
			if err := json.Unmarshal(spec.Params, &selection); err != nil {
				return nil, fmt.Errorf("trace %s params: %w", spec.Kind, err)
			}
		}
		resolved, err := resolveRunTarget(registration, selection.Target, options.PID)
		if err != nil {
			return nil, fmt.Errorf("trace %s params: %w", spec.Kind, err)
		}
		if options.host() && resolved.Seam != target.Host && resolved.Seam != target.Self && resolved.Seam != target.Agent {
			return nil, fmt.Errorf("%s observes a command or a process: give a command or a pid to attach to", spec.Kind)
		}
		armed := aimedSpec{TraceSpec: spec, target: resolved.Seam, checkpoint: spec.Checkpoint}
		if options.PID != 0 && (resolved.Seam == target.Process || resolved.Seam == target.Agent) {
			if armed.Params, err = withDefaultPID(spec.Params, options.PID); err != nil {
				return nil, fmt.Errorf("trace %s params: %w", spec.Kind, err)
			}
		}
		if armed.checkpoint == nil && registration.Checkpoint != nil {
			if armed.checkpoint, err = json.Marshal(registration.Checkpoint); err != nil {
				return nil, fmt.Errorf("trace %s checkpoint params: %w", spec.Kind, err)
			}
		}
		specs = append(specs, armed)
	}
	return specs, nil
}

func resolveRunTarget(registration traces.Registration, selected target.Target, pid int) (target.Target, error) {
	takesPID := selected.Seam == target.Process || selected.Seam == target.Agent || selected.Seam == target.Proxy
	if selected.Seam == "" {
		takesPID = registration.DefaultSeam == target.Proxy || slices.Contains(registration.Seams, target.Process) || slices.Contains(registration.Seams, target.Agent)
	}
	if pid != 0 && takesPID && selected.PID == 0 && selected.Agent == "" && len(selected.Selector) == 0 {
		selected.PID = pid
	}
	var lookupErr error
	available := func(t target.Target) bool {
		var found bool
		found, lookupErr = (seam.AgentSeam[struct{}, struct{}]{}).Available(registration.Name, t)
		return found
	}
	resolved, err := target.Resolve(registration.Name, selected, registration.Seams, registration.DefaultSeam, available)
	if lookupErr != nil {
		return target.Target{}, fmt.Errorf("agent lookup: %w", lookupErr)
	}
	return resolved, err
}

// withDefaultPID sets the target PID unless the params already name one.
func withDefaultPID(params json.RawMessage, pid int) (json.RawMessage, error) {
	values := map[string]any{}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &values); err != nil {
			return nil, fmt.Errorf("want an object: %w", err)
		}
	}
	targetValue := map[string]any{}
	if raw, ok := values["target"]; ok && raw != nil {
		var valid bool
		targetValue, valid = raw.(map[string]any)
		if !valid {
			return nil, errors.New("target must be an object")
		}
	}
	if _, ok := targetValue["pid"]; !ok {
		targetValue["pid"] = pid
	}
	values["target"] = targetValue
	return json.Marshal(values)
}

// alive reports whether pid is a running process this process may signal.
func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// checkpoints reads the run's checkpointed captures, collecting what fails
// rather than ending the run: one failed dump says nothing of the next.
type checkpoints struct {
	mu     sync.Mutex
	errors []string
}

func (c *checkpoints) failed() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.errors
}

// checkpoint reads each checkpointed capture once, for reason. A command's
// process captures are read of the command, unless their params name a
// process, as their root is this process's tree.
func (r *Run) checkpoint(ctx context.Context, reason string) {
	for index, capture := range r.captures {
		spec := r.specs[index]
		if spec.checkpoint == nil {
			continue
		}
		params, err := withValue(spec.checkpoint, "reason", reason)
		if err == nil && spec.target == target.Process && r.cmd != nil && !names(params, "pid", "key") {
			params, err = withValue(params, "pid", r.cmd.Process.Pid)
		}
		if err == nil {
			err = capture.Checkpoint(ctx, params)
		}
		if err != nil {
			r.checkpoints.mu.Lock()
			r.checkpoints.errors = append(r.checkpoints.errors, fmt.Sprintf("%s %s checkpoint: %v", spec.Kind, reason, err))
			r.checkpoints.mu.Unlock()
		}
	}
}

// checkpointEvery reads the checkpointed captures every r.options.Every
// until the run's target is gone.
func (r *Run) checkpointEvery() {
	defer close(r.checkpointed)
	if r.options.Every == 0 || !slices.ContainsFunc(r.specs, func(spec aimedSpec) bool { return spec.checkpoint != nil }) {
		return
	}
	ticker := time.NewTicker(r.options.Every)
	defer ticker.Stop()
	for {
		select {
		case <-r.exited:
			return
		case <-ticker.C:
			r.checkpoint(context.Background(), "interval")
		}
	}
}

func withValue(params json.RawMessage, key string, value any) (json.RawMessage, error) {
	values := map[string]any{}
	if err := json.Unmarshal(params, &values); err != nil {
		return nil, fmt.Errorf("checkpoint params want an object: %w", err)
	}
	values[key] = value
	return json.Marshal(values)
}

func names(params json.RawMessage, keys ...string) bool {
	values := map[string]any{}
	if json.Unmarshal(params, &values) != nil {
		return false
	}
	for _, key := range keys {
		if value, ok := values[key]; ok && value != nil && value != "" && value != float64(0) {
			return true
		}
	}
	return false
}
