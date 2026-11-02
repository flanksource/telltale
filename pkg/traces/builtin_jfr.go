package traces

import (
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/jfr"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

func init() {
	MustRegister(Registration{Name: jfr.KindName, Title: "JVM JFR", Summary: "Profile a JVM with Flight Recorder or async-profiler", Description: "Attach to a local JVM process or select a connected Java agent. Checkpoints decode new profiling samples into the trace store.", Seams: []target.Seam{target.Process, target.Agent}, DefaultSeam: target.Process, Armable: true, Checkpoint: map[string]any{}, New: func(KindDeps) tracing.TracePlugin { return jfr.Kind() }, Examples: []Example{{Args: "--pid 1234 --duration 30s --every 10s", Says: "profile a local JVM"}, {Args: "--seam agent --selector service.name=example --duration 30s", Says: "profile connected Java agents"}}})
}
