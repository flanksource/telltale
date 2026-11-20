package telltale_test

import (
	"time"

	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/target"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("a run's summary", func() {
	started := time.Date(2026, 11, 16, 0, 1, 48, 0, time.UTC)
	const runID = "host20261116T000148Z"

	It("prints the run on one line, then one line per capture with its target, scalar summary and the command that reads it back", func() {
		result := telltale.Result{RunID: runID, StartedAt: started, EndedAt: started.Add(3600 * time.Millisecond), Captures: []telltale.CaptureResult{
			{Kind: "jvm_trace", Ref: tracing.TraceRef{Kind: "jvm_trace", Stream: "S1"}, Summary: map[string]any{"count": 45}, Target: target.Target{Seam: target.Agent, Agent: "80618"}},
			{Kind: "http", Ref: tracing.TraceRef{Kind: "http", Stream: "S2"}, Summary: map[string]any{"entries": 12, "errors": 1, "hosts": []string{"example.com"}, "methods": map[string]int{"GET": 12}}, Target: target.Target{Seam: target.Proxy}},
		}}
		Expect(result.Pretty().String()).To(Equal(
			"Run " + runID + " · 3.6s\n" +
				"  jvm_trace · agent 80618 · 45 records · telltale records jvm_trace --run " + runID + "\n" +
				"  http · proxy · entries 12 · errors 1 · telltale records http --run " + runID))
	})

	It("names the command and its exit code, and lists why the run failed", func() {
		result := telltale.Result{RunID: runID, Command: []string{"make", "test"}, ExitCode: 2, StartedAt: started, EndedAt: started.Add(90 * time.Second),
			CheckpointErrors: []string{"heap: process exited"}, Error: "summarize sql: closed"}
		Expect(result.Pretty().String()).To(Equal(
			"Run " + runID + " · 1m30s · make test exited 2\n" +
				"  ✗ heap: process exited\n" +
				"  ✗ summarize sql: closed"))
	})

	It("names a traced pid, and a capture with nothing recorded yet", func() {
		result := telltale.Result{RunID: runID, PID: 4242, StartedAt: started, EndedAt: started.Add(250 * time.Millisecond), Captures: []telltale.CaptureResult{
			{Kind: "process", Ref: tracing.TraceRef{Kind: "process", Stream: "S3"}, Target: target.Target{PID: 4242}},
		}}
		Expect(result.Pretty().String()).To(Equal(
			"Run " + runID + " · 250ms · pid 4242\n" +
				"  process · pid 4242 · telltale records process --run " + runID))
	})
})
