package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/flanksource/captain/pkg/ai"
	clickyexec "github.com/flanksource/clicky/exec"
	"github.com/flanksource/telltale"
)

func generate(ctx context.Context, o options) (err error) {
	var workDir string
	defer func() {
		if err != nil && workDir != "" {
			data, encodeErr := json.Marshal(map[string]string{"error": err.Error(), "dataDir": o.dataDir, "workDir": workDir})
			if encodeErr != nil {
				err = errors.Join(err, encodeErr)
				return
			}
			err = errors.Join(err, os.WriteFile(filepath.Join(workDir, "demo-failure.json"), append(data, '\n'), 0o600))
			fmt.Fprintln(os.Stderr, "Partial demo artifacts:", workDir)
		}
	}()
	for _, binary := range []string{"git", "java", "javac", "jcmd", "ps"} {
		if _, err := exec.LookPath(binary); err != nil {
			return fmt.Errorf("demo prerequisite: %w", err)
		}
	}
	provider, err := prepareLLM(o.model)
	if err != nil {
		return err
	}
	lab, err := openLab(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if lab != nil {
			err = errors.Join(err, lab.close())
		}
	}()
	if err := os.MkdirAll(filepath.Dir(o.dataDir), 0o700); err != nil {
		return err
	}
	dir, err := os.MkdirTemp(filepath.Dir(o.dataDir), "work-")
	if err != nil {
		return err
	}
	workDir = dir
	local, err := startLocal(ctx, dir)
	if err != nil {
		return err
	}
	defer func() {
		if local != nil {
			err = errors.Join(err, local.close())
		}
	}()
	jvm, err := startJava(ctx, dir)
	if err != nil {
		return err
	}
	defer func() {
		if jvm != nil {
			err = errors.Join(err, jvm.close())
		}
	}()
	if err := lab.create(ctx); err != nil {
		return err
	}
	specs, err := demoSpecs(local.url, lab.name, jvm.cmd.Process.Pid)
	if err != nil {
		return err
	}
	run, err := telltale.Start(ctx, telltale.StartOptions{DataDir: o.dataDir, PID: os.Getpid(), Every: 3 * time.Second, Traces: specs, Connections: lab.connections})
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "Recording demo", run.ID(), "into", run.Dir())
	workErr := exercise(ctx, run, local, lab, provider)
	stopErr := run.Stop()
	_, waitErr := run.Wait()
	err = errors.Join(workErr, stopErr, waitErr, jvm.close(), local.close(), lab.close())
	jvm, local, lab = nil, nil, nil
	verificationCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	return verifyStored(verificationCtx, verification{DataDir: o.dataDir, RunID: run.ID(), Failure: err, Sessions: captureSessions(run.Runtime().Sessions.List())})
}

func exercise(ctx context.Context, run *telltale.Run, local *localWorkload, lab *labWorkload, provider ai.Provider) error {
	for _, capture := range run.Info().Captures {
		if capture.Kind == "terminal" {
			if err := recordTerminal(ctx, capture.Ref.Stream); err != nil {
				return err
			}
		}
	}
	if result := clickyexec.NewExec("git", "--version").WithContext(ctx).Run().Result(); result.Error != nil {
		return result.Error
	}
	if err := lab.deadlock(ctx); err != nil {
		return err
	}
	var held [][]byte
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	until := time.NewTimer(20 * time.Second)
	defer until.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-until.C:
			if err := recordLLM(ctx, provider); err != nil {
				return err
			}
			runtime.KeepAlive(held)
			return nil
		case <-ticker.C:
			held = append(held, make([]byte, 64<<10))
			if err := local.request(ctx); err != nil {
				return err
			}
			if err := lab.query(ctx); err != nil {
				return err
			}
		}
	}
}
