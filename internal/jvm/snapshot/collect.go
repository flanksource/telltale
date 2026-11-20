package snapshot

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/tracing"
)

const maxArtifact = 64 << 20

type caller func(context.Context, string) (json.RawMessage, error)

func Collect(ctx context.Context, options Options) (*Result, error) {
	if err := options.Validate(); err != nil {
		return nil, err
	}
	agent := agentclient.Default.ByID(options.Agent)
	if agent == nil || agent.Hello.Runtime.Lang != "java" {
		return nil, fmt.Errorf("connected Java agent %q is required", options.Agent)
	}
	result, err := collect(ctx, options, workloadTarget(agent.Hello.Resource, agent.Hello.Service), func(ctx context.Context, op string) (json.RawMessage, error) {
		return agent.Call(ctx, agentclient.CallRequest{Op: op, Params: json.RawMessage(`{}`)})
	})
	if err != nil {
		return nil, err
	}
	identity, err := json.Marshal(tracing.MaskAttribute("identity", agent.Hello))
	if err != nil {
		return nil, err
	}
	identity, err = sanitized(identity, false)
	result.add("identity.json", identity, err)
	if options.JFR != "" {
		data, captureErr := profile(ctx, agent, options.JFR)
		result.add("recording.jfr", data, captureErr)
	}
	result.finish()
	return result, nil
}

func collect(ctx context.Context, options Options, target WorkloadTarget, call caller) (*Result, error) {
	if err := options.Validate(); err != nil {
		return nil, err
	}
	result := &Result{Manifest: Manifest{Version: 1, Agent: options.Agent, StartedAt: time.Now().UTC(), Complete: true}, Files: map[string][]byte{}}
	operations := []struct{ op, file string }{
		{"jvm.settings", "settings.json"}, {"jvm.resources", "resources.json"}, {"jvm.flags", "vm-flags.txt"}, {"jvm.metrics", "metrics.json"},
		{"jvm.threads.dump", "threads.txt"}, {"jvm.loaders", "loaders.json"},
		{"jvm.logging.list", "logging.json"}, {"jvm.status", "status.json"},
		{"jvm.telemetry", "telemetry.json"},
	}
	if options.HeapHistogram {
		operations = append(operations, struct{ op, file string }{"jvm.heap.histogram", "heap-histogram.json"})
	}
	for _, operation := range operations {
		if err := ctx.Err(); err != nil {
			result.add(operation.file, nil, err)
			continue
		}
		bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
		raw, err := call(bounded, operation.op)
		cancel()
		var data []byte
		if err == nil {
			data, err = sanitized(raw, strings.HasSuffix(operation.file, ".txt"))
		}
		result.add(operation.file, data, err)
	}
	addTelemetryReport(result)
	if count := options.workloadSamples(); count > 0 {
		addWorkload(ctx, result, target, call, count)
	}
	result.finish()
	return result, nil
}

// addWorkload adds the sampled series and the page that summarises it.
func addWorkload(ctx context.Context, result *Result, target WorkloadTarget, call caller, count int) {
	samples, err := collectWorkload(ctx, call, count)
	var series, page []byte
	if err == nil {
		series, err = json.MarshalIndent(samples, "", "  ")
	}
	result.add("workload.json", series, err)
	if err == nil {
		var panel workloadPanel
		if panel, err = newWorkloadPanel(target, samples); err == nil {
			page, err = workloadHTML(panel)
		}
	}
	result.add("workload.html", page, err)
}

func (r *Result) add(name string, data []byte, err error) {
	total := len(data)
	for _, existing := range r.Files {
		total += len(existing)
	}
	if err == nil && total > 128<<20 {
		err = fmt.Errorf("bundle exceeds 128 MiB")
	}
	if err == nil && len(data) > maxArtifact {
		err = fmt.Errorf("artifact exceeds 64 MiB")
	}
	outcome := Outcome{File: name, Status: "ok"}
	if err != nil {
		outcome.Status = "failed"
		outcome.Error = maskArguments(tracing.MaskAttribute("error", err.Error())).(string)
		r.Manifest.Complete = false
	} else {
		outcome.SHA256 = fmt.Sprintf("%x", sha256.Sum256(data))
		r.Files[name] = data
	}
	r.Manifest.Collectors = append(r.Manifest.Collectors, outcome)
}

func (r *Result) finish() {
	r.Manifest.CompletedAt = time.Now().UTC()
	var summary strings.Builder
	fmt.Fprintf(&summary, "# JVM support snapshot\n\nAgent: %s\n\nComplete: %t\n\n", r.Manifest.Agent, r.Manifest.Complete)
	for _, outcome := range r.Manifest.Collectors {
		fmt.Fprintf(&summary, "- %s: %s %s\n", outcome.File, outcome.Status, outcome.Error)
	}
	summary.WriteString("\nHeap histogram, when requested, includes unreachable objects and can pause the JVM. A timed-out native heap walk may continue in the JVM.\n")
	r.Files["summary.md"] = []byte(summary.String())
}

func (r *Result) Zip() ([]byte, error) {
	manifest, err := json.MarshalIndent(r.Manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	files := make(map[string][]byte, len(r.Files))
	for name, data := range r.Files {
		files[name] = data
	}
	files["manifest.json"] = manifest
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		entry, err := writer.Create(name)
		if err != nil {
			return nil, err
		}
		if _, err := entry.Write(files[name]); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
