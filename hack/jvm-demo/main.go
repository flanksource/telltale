package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type options struct {
	server, workload, agent, artifacts, phase string
	httpTop, sqlTop, http, sql, jms, spans    string
}

type topObservation struct {
	kind              string
	previous, current topSnapshot
}

func main() {
	var opts options
	flag.StringVar(&opts.server, "server", "", "Loopback Telltale server URL")
	flag.StringVar(&opts.workload, "workload", "", "Loopback workload URL")
	flag.StringVar(&opts.agent, "agent", "", "Attached demo agent ID")
	flag.StringVar(&opts.artifacts, "artifacts", "", "Directory for native snapshot and protocol evidence")
	flag.StringVar(&opts.phase, "phase", "", "active, updated, cleared or completed")
	flag.StringVar(&opts.httpTop, "http-top", "", "HTTP Top capture run ID")
	flag.StringVar(&opts.sqlTop, "sql-top", "", "SQL Top capture run ID")
	flag.StringVar(&opts.http, "http", "", "HTTP capture run ID")
	flag.StringVar(&opts.sql, "sql", "", "SQL capture run ID")
	flag.StringVar(&opts.jms, "jms", "", "JMS capture run ID")
	flag.StringVar(&opts.spans, "spans", "", "Mirrored spans capture run ID")
	flag.Parse()
	if err := run(opts); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(opts options) error {
	client, err := newClient(opts)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(opts.artifacts, 0o700); err != nil {
		return fmt.Errorf("create evidence directory: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := client.workloadState(ctx); err != nil {
		return err
	}
	if opts.phase == "completed" {
		err = client.completed(ctx)
	} else {
		err = client.top(ctx)
	}
	if err != nil {
		return fmt.Errorf("demo %s evidence: %w", opts.phase, err)
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"passed": true, "phase": opts.phase, "agent": opts.agent, "artifacts": opts.artifacts})
}

func (c apiClient) top(ctx context.Context) error {
	for _, capture := range []captureRequest{{kind: "http_top", run: c.opts.httpTop}, {kind: "sql_top", run: c.opts.sqlTop}} {
		observation := topObservation{kind: capture.kind}
		if c.opts.phase != "active" {
			previousOptions := c.opts
			previousOptions.phase = "active"
			if c.opts.phase == "cleared" {
				previousOptions.phase = "updated"
			}
			if err := readEvidence(evidenceFile(previousOptions, capture.kind), &observation.previous); err != nil {
				return err
			}
		}
		err := await(ctx, func() error {
			if err := c.get(ctx, jsonRequest{path: recordsPath(capture) + "?view=top&all=true", output: &observation.current}); err != nil {
				return err
			}
			return c.checkTop(observation)
		})
		if err != nil {
			if observation.current.Frame.ID != "" {
				path := filepath.Join(c.opts.artifacts, c.opts.phase+"-"+capture.kind+"-failed.json")
				err = errors.Join(err, writeEvidence(path, observation.current))
			}
			return fmt.Errorf("%s: %w", capture.kind, err)
		}
		if err := writeEvidence(evidenceFile(c.opts, capture.kind), observation.current); err != nil {
			return err
		}
	}
	return nil
}

func (c apiClient) checkTop(observation topObservation) error {
	if c.opts.phase == "active" {
		return checkActive(observation.kind, observation.current)
	}
	if c.opts.phase == "cleared" {
		if observation.current.Frame.Tick <= observation.previous.Frame.Tick {
			return fmt.Errorf("clear snapshot sequence %d must follow held snapshot %d", observation.current.Frame.Tick, observation.previous.Frame.Tick)
		}
		return checkCleared(observation.current)
	}
	if err := checkActive(observation.kind, observation.current); err != nil {
		return err
	}
	return checkUpdated(observation.previous, observation.current)
}

func (c apiClient) completed(ctx context.Context) error {
	var evidence completedEvidence
	err := await(ctx, func() error {
		requests := []struct {
			capture captureRequest
			output  any
		}{
			{captureRequest{kind: "http", run: c.opts.http}, &evidence.HTTP},
			{captureRequest{kind: "sql", run: c.opts.sql}, &evidence.SQL},
			{captureRequest{kind: "jms", run: c.opts.jms}, &evidence.JMS},
			{captureRequest{kind: "spans", run: c.opts.spans}, &evidence.Mirrored},
			{captureRequest{kind: "spans", run: "receiver"}, &evidence.Received},
		}
		for _, request := range requests {
			if err := c.records(ctx, recordsRequest{capture: request.capture, output: request.output}); err != nil {
				return err
			}
		}
		return checkCompleted(evidence)
	})
	if err != nil {
		return errors.Join(err, writeEvidence(filepath.Join(c.opts.artifacts, "completed-failed.json"), evidence))
	}
	return writeEvidence(filepath.Join(c.opts.artifacts, "completed.json"), evidence)
}

func evidenceFile(opts options, kind string) string {
	return filepath.Join(opts.artifacts, opts.phase+"-"+kind+".json")
}

func writeEvidence(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode evidence %s: %w", path, err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write evidence %s: %w", path, err)
	}
	return nil
}

func readEvidence(path string, output any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read previous phase evidence %s: %w", path, err)
	}
	if err := json.Unmarshal(data, output); err != nil {
		return fmt.Errorf("decode previous phase evidence %s: %w", path, err)
	}
	return nil
}

func await(ctx context.Context, check func() error) error {
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		err := check()
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w; last observation: %w", ctx.Err(), err)
		case <-tick.C:
		}
	}
}
