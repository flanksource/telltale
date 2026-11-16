package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"time"

	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/traces/read"
	"github.com/flanksource/telltale/pkg/tracing"
)

type playgroundSource struct {
	info    telltale.RunInfo
	runtime *tracing.Runtime
}

func playgroundSources(ctx context.Context, o options, info telltale.RunInfo) (map[string]playgroundSource, func() error, error) {
	runtime, closeRecording, err := telltale.Reopen(o.dataDir, info.RunID)
	if err != nil {
		return nil, nil, err
	}
	recording := playgroundSource{info: info, runtime: runtime}
	for _, capture := range info.Captures {
		if capture.Kind == "process" {
			return map[string]playgroundSource{"recording": recording, "process": recording}, closeRecording, nil
		}
	}
	if info.Process != nil && info.Process.Tree.Kind == "process" {
		return map[string]playgroundSource{"recording": recording, "process": recording}, closeRecording, nil
	}
	process, err := recordPlaygroundProcess(ctx, o.dataDir)
	if err != nil {
		return nil, nil, errors.Join(err, closeRecording())
	}
	processRuntime, closeProcess, err := telltale.Reopen(o.dataDir, process.RunID)
	if err != nil {
		return nil, nil, errors.Join(err, closeRecording())
	}
	return map[string]playgroundSource{"recording": recording, "process": {info: process, runtime: processRuntime}}, func() error { return errors.Join(closeRecording(), closeProcess()) }, nil
}

func recordPlaygroundProcess(ctx context.Context, dataDir string) (info telltale.RunInfo, err error) {
	file, err := os.CreateTemp(dataDir, "playground-observed-")
	if err != nil {
		return info, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	run, err := telltale.Start(ctx, telltale.StartOptions{DataDir: dataDir, PID: os.Getpid(), Duration: 3 * time.Second,
		Traces: []telltale.TraceSpec{{Kind: "process", Params: json.RawMessage(`{"target":{"seam":"process"},"interval":"500ms","facets":["files"]}`)}}})
	if err != nil {
		return info, err
	}
	defer func() { err = errors.Join(err, run.Stop()) }()
	if _, err := run.Wait(); err != nil {
		return info, err
	}
	info = run.Info()
	if info.Status != telltale.StatusSucceeded {
		return info, fmt.Errorf("local process capture %s: %s", info.RunID, info.Status)
	}
	fmt.Fprintln(os.Stderr, "Recorded current process examples in", info.RunID)
	return info, nil
}

func (s playgroundSource) samples(ctx context.Context, registration traces.Registration) ([]playgroundKind, error) {
	primary := []tracing.TraceRef{}
	for _, capture := range s.info.Captures {
		if capture.Kind == registration.Name {
			primary = append(primary, capture.Ref)
		}
	}
	if s.info.Process != nil {
		for _, ref := range []tracing.TraceRef{s.info.Process.Tree, s.info.Process.Stacks} {
			if ref.Kind == registration.Name {
				primary = append(primary, ref)
			}
		}
	}
	byKind, seen := map[string][]read.Ref{}, map[string]bool{}
	for _, ref := range primary {
		for _, result := range s.runtime.Kinds.CaptureRefs(ref) {
			if seen[result.Stream] {
				continue
			}
			seen[result.Stream] = true
			byKind[result.Kind] = append(byKind[result.Kind], read.Ref{RunID: s.info.RunID, Runtime: s.runtime, TraceRef: result})
		}
	}
	if len(byKind) == 0 {
		return nil, fmt.Errorf("run %s has no recorded %s capture", s.info.RunID, registration.Name)
	}
	items := []playgroundKind{}
	for _, name := range slices.Sorted(maps.Keys(byKind)) {
		plugin, ok := s.runtime.Kinds.Get(name)
		if !ok {
			return nil, fmt.Errorf("no registered presentation for %s", name)
		}
		item, err := playgroundSamples(ctx, byKind[name], playgroundKind{Name: name, Title: plugin.Title(), Description: registration.Summary, SourceRunID: s.info.RunID, rootPID: s.info.PID})
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}
