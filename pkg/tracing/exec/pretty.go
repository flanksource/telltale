package exec

import (
	"errors"
	"time"

	"github.com/flanksource/clicky/api"
	clickyexec "github.com/flanksource/clicky/exec"
)

func (r ExecRecord) result() clickyexec.ExecResult {
	result := clickyexec.ExecResult{Command: r.Name, Args: r.Args, PID: r.PID, ExitCode: r.ExitCode,
		Duration: time.Duration(r.DurationMs * float64(time.Millisecond)), Stdout: r.StdoutTail, Stderr: r.StderrTail}
	if r.Error != "" {
		result.Error = errors.New(r.Error)
	}
	return result
}

func (r ExecRecord) Pretty() api.Text         { return r.result().Pretty() }
func (r ExecRecord) PrettyFull() api.Textable { return r.result().PrettyFull() }
