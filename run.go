package telltale

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/stacks"
)

// ErrNotLive is a Stop of a run this process is not running: finished,
// pruned, never started, or started by another process.
var ErrNotLive = errors.New("the run is not live in this process")

// stopGrace is how long Stop waits after SIGTERM before it SIGKILLs.
const stopGrace = 5 * time.Second

// Run is a live run: its command, its captures and the store they commit to.
type Run struct {
	host        *traces.Host
	options     StartOptions
	specs       []aimedSpec
	startedAt   time.Time
	key         string
	process     *traces.ProcessTrace
	captures    []*traces.Capture
	checkpoints checkpoints
	cmd         *exec.Cmd
	output      *os.File
	stopOnce    sync.Once
	stopErr     error
	// stopping is closed by Stop of an attached run, which owns no process
	// to signal.
	stopping chan struct{}
	exited   chan struct{}
	// checkpointed is closed once no interval checkpoint can start or is in
	// flight, so the captures can be stopped.
	checkpointed chan struct{}
	done         chan struct{}
	result   *Result
	err      error
}

// Start arms options.Traces, starts options.Command or attaches to
// options.PID, and returns while it runs; Wait has what it recorded. ctx
// ending stops the run.
func Start(ctx context.Context, options StartOptions) (*Run, error) {
	if err := options.validate(); err != nil {
		return nil, err
	}
	specs, err := aimedSpecs(options)
	if err != nil {
		return nil, err
	}
	startedAt := time.Now()
	host, err := traces.Open(traces.OpenOptions{WorkDir: options.WorkDir, RunID: traces.RunID(startedAt), Connections: options.Connections})
	if err != nil {
		return nil, err
	}
	run := &Run{
		host: host, options: options, specs: specs, startedAt: startedAt,
		stopping: make(chan struct{}), exited: make(chan struct{}), checkpointed: make(chan struct{}), done: make(chan struct{}),
	}
	if run.key, err = registryKey(host.Dir()); err == nil {
		err = run.start()
	}
	if err != nil {
		return nil, errors.Join(err, run.close())
	}
	live.add(run)
	go run.wait()
	go run.checkpointEvery()
	go func() {
		var deadline <-chan time.Time
		if options.Duration > 0 {
			timer := time.NewTimer(options.Duration)
			defer timer.Stop()
			deadline = timer.C
		}
		select {
		case <-ctx.Done():
		case <-deadline:
		case <-run.done:
			return
		}
		// A refused signal leaves the command to exit by itself; Wait reports how it ended.
		_ = run.Stop()
	}()
	return run, nil
}

// ID is the run's id, the name of its store.
func (r *Run) ID() string { return r.host.RunID() }

// Dir is the run's store directory.
func (r *Run) Dir() string { return r.host.Dir() }

// Runtime serves the run's store and its running captures, such as through
// traceserve, until the run is done.
func (r *Run) Runtime() *tracing.Runtime { return r.host.Runtime() }

// Done is closed once the run finished and its store is closed.
func (r *Run) Done() <-chan struct{} { return r.done }

// Wait waits for the command to exit and returns what the run recorded; a
// command that exits non-zero is reported in Result.ExitCode, not as an error.
func (r *Run) Wait() (*Result, error) {
	<-r.done
	return r.result, r.err
}

// Info describes the run as it is now.
func (r *Run) Info() RunInfo {
	select {
	case <-r.done:
		if r.err != nil {
			return RunInfo{
				RunID: r.ID(), Dir: r.Dir(), Status: StatusUnfinished, Command: r.options.Command, PID: r.options.PID,
				StartedAt: r.startedAt, Unavailable: r.err.Error(),
			}
		}
		return resultInfo(r.result)
	default:
	}
	captures := make([]CaptureResult, 0, len(r.captures))
	for _, capture := range r.captures {
		captures = append(captures, CaptureResult{Kind: capture.Kind(), Ref: capture.Ref(0, 0)})
	}
	info := RunInfo{
		RunID: r.ID(), Dir: r.Dir(), Status: StatusRunning, Command: r.options.Command, PID: r.options.PID, StartedAt: r.startedAt,
		Captures: captures, Process: r.processRefs(), CheckpointErrors: r.checkpoints.failed(),
	}
	if r.process != nil {
		info.Live = &LiveRun{Sessions: r.process.Sessions()}
	}
	return info
}

// processRefs are the process trace's streams; nil for a host run, which
// traces no process tree.
func (r *Run) processRefs() *traces.ProcessRefs {
	if r.process == nil {
		return nil
	}
	refs := r.process.Refs()
	return &refs
}

// Stop ends the command: SIGTERM, then SIGKILL if it has not exited within
// stopGrace. Under TraceRootCommand the command's whole process group is
// signalled. An attached run stops tracing and leaves its process running.
// The run still summarizes its captures and writes its manifest.
func (r *Run) Stop() error {
	r.stopOnce.Do(func() {
		if r.cmd == nil {
			close(r.stopping)
			return
		}
		r.stopErr = r.signal(syscall.SIGTERM)
		go func() {
			select {
			case <-r.exited:
			case <-time.After(stopGrace):
				_ = r.signal(syscall.SIGKILL) // the SIGTERM error, if any, is already reported
			}
		}()
	})
	return r.stopErr
}

func (r *Run) signal(sig syscall.Signal) error {
	pid := r.cmd.Process.Pid
	if r.options.TraceRoot == TraceRootCommand {
		pid = -pid
	}
	if err := syscall.Kill(pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("stop run %s: signal %s: %w", r.ID(), sig, err)
	}
	return nil
}

// start arms the captures and starts the command and the process trace, the
// trace first when it is rooted at this process, the command first when it is
// rooted at the command. An attached run traces its process's tree, and a
// host run only its captures.
func (r *Run) start() error {
	env, err := r.arm()
	if err != nil {
		return err
	}
	if r.options.host() {
		return nil
	}
	if r.options.PID != 0 {
		return r.startProcessTrace(r.options.PID)
	}
	if r.cmd, err = r.command(env); err != nil {
		return err
	}
	if r.options.TraceRoot == TraceRootSelf {
		if err := r.startProcessTrace(os.Getpid()); err != nil {
			return err
		}
		return r.startCommand()
	}
	if err := r.startCommand(); err != nil {
		return err
	}
	if err := r.startProcessTrace(r.cmd.Process.Pid); err != nil {
		killErr := r.signal(syscall.SIGKILL)
		_ = r.cmd.Wait() // reaps the command killed above; its exit status is moot
		return errors.Join(err, killErr)
	}
	return nil
}

// arm starts every capture and returns the environment they export, refusing
// two captures that export one variable differently.
func (r *Run) arm() (map[string]string, error) {
	env := map[string]string{}
	for _, spec := range r.specs {
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

func (r *Run) command(env map[string]string) (*exec.Cmd, error) {
	cmd := exec.Command(r.options.Command[0], r.options.Command[1:]...)
	cmd.Dir = r.options.Dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = r.options.Stdin, r.options.Stdout, r.options.Stderr
	if r.options.Stdout == nil && r.options.Stderr == nil {
		output, err := os.Create(filepath.Join(r.Dir(), OutputFile))
		if err != nil {
			return nil, fmt.Errorf("create run output: %w", err)
		}
		r.output = output
		cmd.Stdout, cmd.Stderr = output, output
	}
	if r.options.TraceRoot == TraceRootCommand {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	cmd.Env = os.Environ()
	for _, name := range slices.Sorted(maps.Keys(env)) {
		cmd.Env = append(cmd.Env, name+"="+env[name])
	}
	return cmd, nil
}

func (r *Run) startCommand() error {
	if err := r.cmd.Start(); err != nil {
		return fmt.Errorf("run %s: %w", r.options.Command[0], err)
	}
	return nil
}

func (r *Run) startProcessTrace(rootPID int) error {
	var err error
	if r.process, err = r.host.StartProcessTrace(traces.ProcessTraceOptions{RootPID: rootPID, Facets: r.options.Facets}); err != nil {
		return fmt.Errorf("process trace: %w", err)
	}
	return nil
}

// wait waits for the command, or for an attached run to stop, summarizes the
// captures, closes the store and writes the manifest.
func (r *Run) wait() {
	var exitCode int
	var err error
	if r.cmd != nil {
		exitCode, err = commandExitCode(r.cmd.Wait(), r.options.Command[0])
	} else {
		r.waitAttached()
	}
	close(r.exited)
	<-r.checkpointed
	if r.cmd == nil && (r.options.host() || alive(r.options.PID)) {
		r.checkpoint(context.Background(), "stop")
	}
	result := &Result{
		RunID: r.ID(), Dir: r.Dir(), Command: r.options.Command, PID: r.options.PID, StartedAt: r.startedAt, EndedAt: time.Now(),
		ExitCode: exitCode, Process: r.processRefs(),
	}
	if err == nil {
		result.Captures, err = r.summarize(context.Background())
		result.CheckpointErrors = r.checkpoints.failed()
	}
	if err = errors.Join(err, r.close()); err == nil {
		err = writeManifest(result)
	}
	if err == nil {
		r.result = result
	}
	r.err = err
	live.remove(r)
	close(r.done)
}

// attachedPoll is how often an attached run checks its process is running.
const attachedPoll = 250 * time.Millisecond

// waitAttached waits for an attached run to be stopped or its process to
// exit, and a host run to be stopped.
func (r *Run) waitAttached() {
	ticker := time.NewTicker(attachedPoll)
	defer ticker.Stop()
	for {
		select {
		case <-r.stopping:
			return
		case <-ticker.C:
			if !r.options.host() && !alive(r.options.PID) {
				return
			}
		}
	}
}

func commandExitCode(err error, program string) (int, error) {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	if err != nil {
		return 0, fmt.Errorf("run %s: %w", program, err)
	}
	return 0, nil
}

// summarize stops every capture, committing its stream, and summarizes it.
func (r *Run) summarize(ctx context.Context) ([]CaptureResult, error) {
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

// close takes a last reading of the process capture — under TraceRootSelf
// telltale's final goroutines into its stacks — re-observes the tree, stops
// the run's captures and closes its store, which stays on disk to be served;
// the oldest runs' stores beyond traces.KeepRuns go.
func (r *Run) close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var errs []error
	if r.process != nil {
		if r.options.TraceRoot == TraceRootSelf && r.cmd != nil {
			errs = append(errs, r.process.CheckpointStacks(ctx, stacks.CheckpointParams{PID: os.Getpid(), Transport: stacks.TransportInProcess, Reason: "final"}))
		}
		errs = append(errs, r.process.CheckpointTree(ctx), r.process.Stop(ctx))
	}
	if r.output != nil {
		errs = append(errs, r.output.Close())
	}
	errs = append(errs, r.host.Close(ctx), traces.Prune(r.options.WorkDir, traces.KeepRuns))
	return errors.Join(errs...)
}

// registry is the runs this process is running, by the absolute path of
// their store, which is how Stop, Get and List find a run from its work dir
// and id.
type registry struct {
	mu   sync.Mutex
	runs map[string]*Run
}

var live = &registry{runs: map[string]*Run{}}

func registryKey(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve run store %s: %w", dir, err)
	}
	return abs, nil
}

func (g *registry) add(run *Run) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.runs[run.key] = run
}

func (g *registry) remove(run *Run) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.runs, run.key)
}

func (g *registry) get(dir string) (*Run, bool, error) {
	key, err := registryKey(dir)
	if err != nil {
		return nil, false, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	run, ok := g.runs[key]
	return run, ok, nil
}

// Live is the run runID of workDir if this process is running it.
func Live(workDir, runID string) (*Run, bool, error) {
	return live.get(traces.StoreDir(workDir, runID))
}

// Stop stops the run options name, ErrNotLive when this process is not
// running it.
func Stop(options StopOptions) error {
	run, ok, err := Live(options.WorkDir, options.RunID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("stop run %s: %w", options.RunID, ErrNotLive)
	}
	return run.Stop()
}

// StopOptions name a run to stop.
type StopOptions struct {
	WorkDir string
	RunID   string
}
