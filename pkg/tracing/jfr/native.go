package jfr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	clickyexec "github.com/flanksource/clicky/exec"
	"github.com/flanksource/commons-db/query"
)

type NativeRecorder struct{ Dir string }
type nativeRecording struct {
	options         Options
	name, dir, file string
}

func (n NativeRecorder) Start(ctx context.Context, o Options, name string) (Recording, error) {
	if o.PID <= 0 {
		return nil, fmt.Errorf("JFR process capture requires a JVM pid")
	}
	if err := o.Validate(); err != nil {
		return nil, err
	}
	root := n.Dir
	if root == "" {
		root = ".tmp/jfr"
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(root, "capture-")
	if err != nil {
		return nil, err
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	r := &nativeRecording{options: o, name: name, dir: dir, file: filepath.Join(dir, "recording.jfr")}
	switch o.Engine {
	case EngineJFR:
		_, err = r.command(ctx, "jcmd", strconv.Itoa(o.PID), "JFR.start", "name="+strconv.Quote(name), "settings="+strconv.Quote(o.Settings))
	case EngineAsyncProfiler:
		var status string
		status, err = r.command(ctx, r.asprof(), "status", strconv.Itoa(o.PID))
		if err == nil && !strings.Contains(strings.ToLower(status), "not active") {
			err = fmt.Errorf("async-profiler is already active: %s", status)
		}
		if err == nil {
			args := []string{"start", "-e", o.Event, "-o", "jfr", "-f", r.file}
			if o.Interval > 0 {
				args = append(args, "-i", o.Interval.String())
			}
			args = append(args, strconv.Itoa(o.PID))
			_, err = r.command(ctx, r.asprof(), args...)
		}
	}
	if err != nil {
		return nil, errors.Join(err, os.RemoveAll(dir))
	}
	return r, nil
}

func (r *nativeRecording) asprof() string {
	if r.options.AsyncProfiler != "" {
		return r.options.AsyncProfiler
	}
	return "asprof"
}
func (r *nativeRecording) command(ctx context.Context, tool string, args ...string) (string, error) {
	process := clickyexec.NewExec(tool, args...).WithContext(ctx).WithCaptureLimit(1 << 20).Run()
	output := process.GetStdout() + process.GetStderr()
	if process.Err != nil {
		return output, fmt.Errorf("%s JVM %d: %w: %s", tool, r.options.PID, process.Err, output)
	}
	if tool == "jcmd" && len(args) > 1 {
		acknowledgment := map[string]string{"JFR.start": "Started recording", "JFR.dump": "Dumped recording", "JFR.stop": "Stopped recording"}[args[1]]
		if acknowledgment == "" || !strings.Contains(output, acknowledgment) {
			return output, fmt.Errorf("jcmd %s JVM %d did not confirm success: %s", args[1], r.options.PID, output)
		}
	}
	return output, nil
}
func (r *nativeRecording) Dump(ctx context.Context) ([]byte, error) {
	var err error
	if r.options.Engine == EngineJFR {
		_, err = r.command(ctx, "jcmd", strconv.Itoa(r.options.PID), "JFR.dump", "name="+strconv.Quote(r.name), "filename="+strconv.Quote(r.file))
	} else {
		_, err = r.command(ctx, r.asprof(), "dump", "-o", "jfr", "-f", r.file, strconv.Itoa(r.options.PID))
	}
	if err != nil {
		return nil, err
	}
	return os.ReadFile(r.file)
}
func (r *nativeRecording) Stop(ctx context.Context) error {
	var err error
	if r.options.Engine == EngineJFR {
		_, err = r.command(ctx, "jcmd", strconv.Itoa(r.options.PID), "JFR.stop", "name="+strconv.Quote(r.name))
	} else {
		_, err = r.command(ctx, r.asprof(), "stop", strconv.Itoa(r.options.PID))
	}
	if err != nil {
		return err
	}
	return os.RemoveAll(r.dir)
}
func (r *nativeRecording) Handle() string { return r.name }
func (r *nativeRecording) Metadata() []query.SessionMetadata {
	return []query.SessionMetadata{{Name: "jfr.pid", Label: "JVM PID", Value: strconv.Itoa(r.options.PID)}}
}
