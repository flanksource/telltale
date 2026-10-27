package rules

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	captaincmux "github.com/flanksource/captain/pkg/cmux"
	clickyexec "github.com/flanksource/clicky/exec"
	clickyprocess "github.com/flanksource/clicky/process"
	"github.com/flanksource/telltale/internal/processcontrol"
	"github.com/flanksource/telltale/pkg/tracing/process"
)

type HookRunner interface {
	Run(context.Context, Decision) HookResult
}
type Executor struct {
	cmuxTop  func(context.Context) (captaincmux.TopSnapshot, error)
	cmuxSend func(context.Context, string, string, string) error
}

func NewExecutor() *Executor {
	return &Executor{cmuxTop: readCmuxTop, cmuxSend: sendCmuxKey}
}

func (e *Executor) Run(ctx context.Context, d Decision) HookResult {
	if len(d.Targets.Processes) == 0 && len(d.Targets.Sessions) == 0 {
		return HookResult{Status: "no-targets"}
	}
	timeout := 10 * time.Second
	if d.Hook.Timeout != "" {
		var err error
		timeout, err = time.ParseDuration(d.Hook.Timeout)
		if err != nil {
			return HookResult{Status: "failed", Error: err.Error()}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	snapshot, err := clickyprocess.Discover(ctx, clickyprocess.SnapshotOptions{})
	if err != nil {
		return HookResult{Status: "failed", Error: err.Error()}
	}
	result := HookResult{Status: "succeeded"}
	for _, p := range d.Targets.Processes {
		if err := verifyProcess(snapshot, p.Key); err != nil {
			result.Targets = append(result.Targets, TargetResult{Key: p.Key, Error: err.Error()})
		}
	}
	for _, s := range d.Targets.Sessions {
		if s.Error != "" {
			result.Targets = append(result.Targets, TargetResult{Key: s.Key, Error: s.Error})
		}
		if len(s.Anchors) == 0 {
			result.Targets = append(result.Targets, TargetResult{Key: s.Key, Error: "session has no verified live anchor"})
		}
		for _, key := range s.Anchors {
			if err := verifyProcess(snapshot, key); err != nil {
				result.Targets = append(result.Targets, TargetResult{Key: s.Key, Error: err.Error()})
			}
		}
	}
	if len(result.Targets) > 0 {
		result.Status = "failed"
		return result
	}
	if d.Hook.Type == "exec" {
		return runExecutable(ctx, d)
	}
	for _, p := range d.Targets.Processes {
		request := processcontrol.Request{Key: p.Key, Action: d.Hook.Type, Nice: d.Hook.Nice, Signal: d.Hook.Signal, LowerOnly: d.Hook.Type == "renice"}
		err := processcontrol.Apply(ctx, request)
		item := TargetResult{Key: p.Key}
		if err != nil {
			item.Error = err.Error()
			result.Status = "failed"
		}
		result.Targets = append(result.Targets, item)
	}
	for _, s := range d.Targets.Sessions {
		var err error
		if d.Hook.Via == "captain" {
			err = interruptCaptain(ctx, d.Hook, s)
		} else {
			err = e.interruptCmux(ctx, d.Hook, s)
		}
		item := TargetResult{Key: s.Key}
		if err != nil {
			item.Error = err.Error()
			result.Status = "failed"
		}
		result.Targets = append(result.Targets, item)
	}
	return result
}
func verifyProcess(snapshot *clickyprocess.Snapshot, key string) error {
	pid, err := process.ParseKey(key)
	if err != nil {
		return err
	}
	p, ok := snapshot.Get(pid)
	if !ok || p.StartedAt == nil || process.Key(p) != key {
		return fmt.Errorf("%s: process exited or identity changed", key)
	}
	if pid <= 1 || pid == os.Getpid() || p.UID != os.Geteuid() {
		return fmt.Errorf("%s: protected process or another owner", key)
	}
	for parent := os.Getpid(); parent > 1; {
		ancestor, ok := snapshot.Get(parent)
		if !ok || ancestor.PPID == parent {
			break
		}
		parent = ancestor.PPID
		if parent == pid {
			return fmt.Errorf("%s: monitor ancestor is protected", key)
		}
	}
	return nil
}
func runExecutable(ctx context.Context, d Decision) HookResult {
	payload, err := json.Marshal(d)
	if err != nil {
		return HookResult{Status: "failed", Error: err.Error()}
	}
	if len(d.Hook.Command) == 0 {
		return HookResult{Status: "failed", Error: "missing command"}
	}
	timeout := 10 * time.Second
	if d.Hook.Timeout != "" {
		timeout, err = time.ParseDuration(d.Hook.Timeout)
		if err != nil {
			return HookResult{Status: "failed", Error: err.Error()}
		}
	}
	binary, err := exec.LookPath(d.Hook.Command[0])
	if err != nil {
		return HookResult{Status: "failed", Error: err.Error()}
	}
	if clickyexec.ContainsShellOperators(binary) {
		return HookResult{Status: "failed", Error: "executable path contains shell operators"}
	}
	command := clickyexec.NewExec(binary, d.Hook.Command[1:]...).WithContext(ctx).WithTimeout(timeout).WithProcessGroup().WithCaptureLimit(64 * 1024).WithStdin(bytes.NewReader(payload)).Run()
	result := command.Result()
	output := HookResult{Status: "succeeded", Stdout: command.GetStdout(), Stderr: command.GetStderr()}
	code := command.ExitCode()
	output.ExitCode = &code
	if result.Error != nil {
		output.Status = "failed"
		output.Error = result.Error.Error()
	}
	return output
}
func interruptCaptain(ctx context.Context, h HookSpec, s SessionRef) (err error) {
	endpoint := strings.TrimRight(h.Endpoint, "/") + "/api/captain/sessions/" + url.PathEscape(s.ID)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/chat", nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, response.Body.Close()) }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("captain session %s capability check: HTTP %d", s.ID, response.StatusCode)
	}
	var chat struct {
		Capabilities struct {
			Interrupt bool `json:"interrupt"`
		} `json:"capabilities"`
		State struct {
			Status string `json:"status"`
		} `json:"state"`
	}
	if err := json.NewDecoder(response.Body).Decode(&chat); err != nil {
		return err
	}
	if !chat.Capabilities.Interrupt || chat.State.Status != "running" {
		return fmt.Errorf("captain session %s cannot be interrupted", s.ID)
	}
	request, err = http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/interrupt", nil)
	if err != nil {
		return err
	}
	interrupted, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, interrupted.Body.Close()) }()
	if interrupted.StatusCode != http.StatusAccepted {
		return fmt.Errorf("captain interrupt session %s: HTTP %d", s.ID, interrupted.StatusCode)
	}
	return nil
}
func (e *Executor) interruptCmux(ctx context.Context, h HookSpec, s SessionRef) error {
	if s.Workspace == "" || s.Surface == "" {
		return fmt.Errorf("session %s has no cmux workspace/surface", s.Key)
	}
	snapshot, err := e.cmuxTop(ctx)
	if err != nil {
		return err
	}
	target, err := snapshot.Resolve(captaincmux.Selector{WorkspaceID: s.Workspace, SurfaceID: s.Surface})
	if err != nil {
		return err
	}
	for _, key := range s.Anchors {
		pid, err := process.ParseKey(key)
		if err != nil {
			return err
		}
		if !slices.Contains(target.PIDs, pid) {
			return fmt.Errorf("session %s anchor %s moved from its cmux surface", s.Key, key)
		}
	}
	return e.cmuxSend(ctx, s.Workspace, s.Surface, h.Key)
}
