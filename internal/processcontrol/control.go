package processcontrol

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	clickyprocess "github.com/flanksource/clicky/process"
	"github.com/flanksource/telltale/pkg/tracing/process"
)

var ErrInvalid = errors.New("invalid process action")
var ErrStale = errors.New("process exited or identity changed")

type Request struct {
	Key       string `json:"key"`
	Action    string `json:"action"`
	Nice      *int   `json:"nice,omitempty"`
	Signal    string `json:"signal,omitempty"`
	LowerOnly bool   `json:"lowerOnly,omitempty"`
}

func Apply(ctx context.Context, request Request) error {
	pid, err := process.ParseKey(request.Key)
	if err != nil || !strings.Contains(request.Key, "@") || pid <= 1 || pid == os.Getpid() {
		return fmt.Errorf("%w: a start-qualified key for a process other than init or this server is required", ErrInvalid)
	}
	switch request.Action {
	case "terminate":
		if request.Nice != nil {
			return fmt.Errorf("%w: terminate accepts no nice value", ErrInvalid)
		}
	case "renice":
		if request.Nice == nil || *request.Nice < -20 || *request.Nice > 19 {
			return fmt.Errorf("%w: nice must be -20..19", ErrInvalid)
		}
	case "signal":
		if request.Nice != nil || !validSignal(request.Signal) {
			return fmt.Errorf("%w: signal must be SIGHUP, SIGINT, SIGTERM or SIGKILL", ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: action must be terminate, renice or signal", ErrInvalid)
	}
	if request.Action != "signal" && request.Signal != "" || request.Action != "renice" && request.LowerOnly {
		return fmt.Errorf("%w: incompatible action parameters", ErrInvalid)
	}
	snapshot, err := clickyprocess.Discover(ctx, clickyprocess.SnapshotOptions{})
	if err != nil {
		return fmt.Errorf("verify process: %w", err)
	}
	p, ok := snapshot.Get(pid)
	if !ok || p.StartedAt == nil || process.Key(p) != request.Key {
		return fmt.Errorf("pid %d: %w", pid, ErrStale)
	}
	if err := act(pid, request); err != nil {
		return fmt.Errorf("%s pid %d: %w", request.Action, pid, err)
	}
	return nil
}
