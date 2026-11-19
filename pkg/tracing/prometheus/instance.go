package prometheus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/flanksource/clicky/exec"
	"github.com/flanksource/deps"
	"github.com/flanksource/telltale/internal/lifetime"
	"github.com/flanksource/telltale/pkg/tracing/process/starttime"
	"github.com/gofrs/flock"
	psprocess "github.com/shirou/gopsutil/v4/process"
)

type instanceState struct {
	Port      int       `json:"port"`
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"startedAt"`
}

func (s instanceState) address() string {
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(s.Port))
}

func (s instanceState) server(paths ManagedPaths) Server {
	return Server{URL: "http://" + s.address(), Job: Job, FileSDDir: paths.FileSD}
}

func (s instanceState) running(ctx context.Context) (bool, error) {
	exists, err := psprocess.PidExistsWithContext(ctx, int32(s.PID))
	if err != nil || !exists {
		return false, err
	}
	startedAt, err := starttime.Of(s.PID)
	if errors.Is(err, psprocess.ErrorProcessNotRunning) || errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return s.StartedAt.Equal(startedAt), err
}

func stateFile(paths ManagedPaths) string { return filepath.Join(paths.serviceDir(), "state.json") }

func readInstance(paths ManagedPaths) (*instanceState, error) {
	path := stateFile(paths)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read VictoriaMetrics state %s: %w", path, err)
	}
	var state instanceState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("decode VictoriaMetrics state %s: %w", path, err)
	}
	if state.Port < 1 || state.Port > 65535 || state.PID < 1 || state.PID > 1<<31-1 || state.StartedAt.IsZero() {
		return nil, fmt.Errorf("invalid VictoriaMetrics process identity in %s", path)
	}
	return &state, nil
}

func (s instanceState) save(paths ManagedPaths) error {
	file, err := os.CreateTemp(paths.serviceDir(), ".state-*.tmp")
	if err != nil {
		return err
	}
	writeErr := json.NewEncoder(file).Encode(s)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return errors.Join(err, os.Remove(file.Name()))
	}
	if err := os.Rename(file.Name(), stateFile(paths)); err != nil {
		return errors.Join(err, os.Remove(file.Name()))
	}
	return nil
}

func lockInstance(ctx context.Context, paths ManagedPaths) (func() error, error) {
	lock := flock.New(filepath.Join(paths.serviceDir(), "instance.lock"))
	locked, err := lock.TryLockContext(ctx, 100*time.Millisecond)
	if err != nil || !locked {
		return nil, fmt.Errorf("lock VictoriaMetrics at %s: %w", paths.serviceDir(), errors.Join(err, ctx.Err(), lock.Close()))
	}
	return lock.Close, nil
}

func openInstance(ctx context.Context, paths ManagedPaths) (*lease, error) {
	state, err := readInstance(paths)
	if err != nil {
		return nil, err
	}
	if state != nil {
		running, err := state.running(ctx)
		if err != nil {
			return nil, fmt.Errorf("check VictoriaMetrics process %d: %w", state.PID, err)
		}
		if running {
			server := state.server(paths)
			if err := newClient(server).health(ctx); err != nil {
				return nil, err
			}
			return &lease{paths: paths, server: server}, nil
		}
	} else {
		state = &instanceState{}
	}
	return startInstance(ctx, paths, *state)
}

func installVictoriaMetrics(ctx context.Context, dir string) (_ string, err error) {
	bin := filepath.Join(dir, "bin", Version, "victoria-metrics")
	if info, err := os.Stat(bin); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
			return "", fmt.Errorf("VictoriaMetrics binary is not executable: %s", bin)
		}
		return bin, ctx.Err()
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("check VictoriaMetrics binary: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(bin), 0o700); err != nil {
		return "", err
	}
	staging, err := os.MkdirTemp(dir, ".install-*")
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(staging)) }()
	asset := fmt.Sprintf("victoria-metrics-%s-%s-%s.tar.gz", runtime.GOOS, runtime.GOARCH, Version)
	_, err = deps.InstallWithContext(ctx, "VictoriaMetrics/VictoriaMetrics#"+asset, Version,
		deps.WithStrictChecksum(true), deps.WithTimeout(2*time.Minute), deps.WithBinDir(filepath.Join(staging, "bin")),
		deps.WithAppDir(filepath.Join(staging, "apps")), deps.WithTmpDir(filepath.Join(staging, "tmp")),
		deps.WithCacheDir(filepath.Join(dir, "downloads")))
	if err != nil {
		return "", fmt.Errorf("install VictoriaMetrics %s (%s): %w", Version, asset, err)
	}
	if err := os.Rename(filepath.Join(staging, "bin", "victoria-metrics"), bin); err != nil {
		return "", fmt.Errorf("publish VictoriaMetrics binary: %w", err)
	}
	return bin, ctx.Err()
}

func startInstance(ctx context.Context, paths ManagedPaths, state instanceState) (_ *lease, err error) {
	if err := os.MkdirAll(paths.FileSD, 0o700); err != nil {
		return nil, fmt.Errorf("create VictoriaMetrics target directory: %w", err)
	}
	config := filepath.Join(paths.serviceDir(), "scrape.yaml")
	scrapes := fmt.Sprintf("scrape_configs:\n  - job_name: %s\n    file_sd_configs:\n      - files: [%q]\n", Job, filepath.Join(paths.FileSD, "*.json"))
	if err := os.WriteFile(config, []byte(scrapes), 0o600); err != nil {
		return nil, fmt.Errorf("write VictoriaMetrics scrape configuration: %w", err)
	}
	binary, err := installVictoriaMetrics(ctx, paths.serviceDir())
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", state.address())
	if err != nil {
		return nil, fmt.Errorf("allocate VictoriaMetrics listener at %s: %w", state.address(), err)
	}
	state.Port = listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		return nil, err
	}
	server := state.server(paths)
	processCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	process := exec.NewExec(binary, "-httpListenAddr="+state.address(), "-storageDataPath="+filepath.Join(paths.serviceDir(), "data"),
		"-retentionPeriod=15d", "-promscrape.config="+config, "-promscrape.fileSDCheckInterval=1s", "-search.latencyOffset=0s").
		WithContext(processCtx).WithProcessGroup().WithCaptureLimit(64 << 10)
	run := lifetime.Start("VictoriaMetrics", func() error { return process.Run().Err })
	stop := func() error {
		cancel()
		err := run.Wait()
		if !errors.Is(err, context.Canceled) {
			return err
		}
		return nil
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, stop())
		}
	}()
	if err := awaitReady(ctx, server, process, run); err != nil {
		return nil, err
	}
	state.PID = process.Pid()
	state.StartedAt, err = starttime.Of(state.PID)
	if err != nil {
		return nil, fmt.Errorf("identify VictoriaMetrics process %d: %w", state.PID, err)
	}
	if err := state.save(paths); err != nil {
		return nil, fmt.Errorf("publish VictoriaMetrics state: %w", err)
	}
	return &lease{paths: paths, server: server, stop: stop}, nil
}

func awaitReady(ctx context.Context, server Server, process *exec.Process, run *lifetime.Service) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	client := newClient(server)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-run.Done():
			return fmt.Errorf("VictoriaMetrics exited during startup: %v\n%s", run.Wait(), process.GetStderr())
		case <-ctx.Done():
			return fmt.Errorf("VictoriaMetrics did not become ready at %s: %w\n%s", server.URL, ctx.Err(), process.GetStderr())
		case <-ticker.C:
			if process.IsRunning() && client.health(ctx) == nil && process.IsRunning() {
				return nil
			}
		}
	}
}
