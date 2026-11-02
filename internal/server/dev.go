package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/flanksource/clicky/exec"
	"github.com/flanksource/clicky/task"

	"github.com/flanksource/telltale/internal/lifetime"
)

type devOptions struct {
	Options
	apiURL string
	cancel context.CancelCauseFunc
}

type viteServer struct {
	target *url.URL
	cancel context.CancelFunc
	run    *lifetime.Service
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
	run := lifetime.Start("Vite", func() error {
		process.Run()
		if ctx.Err() != nil {
			return nil
		}
		err := fmt.Errorf("vite stopped unexpectedly: %w", errors.Join(process.Err, errors.New(process.GetStderr())))
		options.cancel(err)
		return err
	})
	vite := &viteServer{cancel: cancel, run: run}
	if err := process.WaitForStdout("http://localhost:", 30*time.Second); err != nil {
		return nil, errors.Join(fmt.Errorf("start Vite: %w\n%s", err, process.GetStderr()), vite.stop())
	}
	match := regexp.MustCompile(`Local:\s+(http://localhost:[0-9]+/)`).FindStringSubmatch(ansi.Strip(process.GetStdout()))
	if len(match) != 2 {
		return nil, errors.Join(fmt.Errorf("vite did not report a local URL: %s", process.GetStdout()), vite.stop())
	}
	if vite.target, err = url.Parse(match[1]); err != nil {
		return nil, errors.Join(fmt.Errorf("parse Vite URL: %w", err), vite.stop())
	}
	return vite, nil
}

func (v *viteServer) stop() error {
	v.cancel()
	return v.run.Wait()
}

func (v *viteServer) proxy() http.Handler { return httputil.NewSingleHostReverseProxy(v.target) }
