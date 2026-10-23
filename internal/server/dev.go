package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/flanksource/clicky/exec"
	"github.com/flanksource/clicky/task"
	cctx "github.com/flanksource/commons/context"
)

type devOptions struct {
	Options
	apiURL string
	cancel context.CancelCauseFunc
}

type viteServer struct {
	target *url.URL
	cancel context.CancelFunc
	run    task.TypedTask[bool]
}

func startVite(ctx context.Context, options devOptions) (*viteServer, error) {
	directory, err := filepath.Abs(options.WebDirectory)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(directory, "vite.config.ts")); err != nil {
		return nil, fmt.Errorf("--dev requires the source checkout; web-dir %s: %w", directory, err)
	}
	ctx, cancel := context.WithCancel(ctx)
	process := exec.NewExec("pnpm", "--dir", directory, "exec", "vite", "--host", "localhost", "--port", strconv.Itoa(options.UIPort), "--strictPort").
		WithContext(ctx).WithProcessGroup().WithEnv(map[string]string{"TELLTALE_DEV": "1", "TELLTALE_API_URL": options.apiURL}).
		Stream(options.Root.OutOrStdout(), options.Root.ErrOrStderr())
	task.SetNoRender(true)
	run := task.StartTask("Vite", func(_ cctx.Context, _ *task.Task) (bool, error) {
		process.Run()
		if ctx.Err() != nil {
			return true, nil
		}
		err := fmt.Errorf("vite stopped unexpectedly: %w", errors.Join(process.Err, errors.New(process.GetStderr())))
		options.cancel(err)
		return false, err
	}, task.WithContext(ctx))
	vite := &viteServer{target: &url.URL{Scheme: "http", Host: net.JoinHostPort("localhost", strconv.Itoa(options.UIPort))}, cancel: cancel, run: run}
	if err := process.WaitForStdout("Local:", 30*time.Second); err != nil {
		return nil, errors.Join(fmt.Errorf("start Vite: %w\n%s", err, process.GetStderr()), vite.stop())
	}
	return vite, nil
}

func (v *viteServer) stop() error {
	v.cancel()
	_, err := v.run.GetResult()
	return err
}

func (v *viteServer) proxy() http.Handler { return httputil.NewSingleHostReverseProxy(v.target) }
