package traces

import (
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/exec"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

func init() {
	MustRegister(Registration{Name: exec.Kind, Title: "Executions", Summary: "Record observed command executions", Description: "Records Clicky executions in telltale itself or an instrumented agent, including output tails and environment variable names.", Examples: []Example{{Args: "--pid 1234 --name git --duration 1m", Says: "record git executions from the agent in process 1234"}}, Seams: []target.Seam{target.Self, target.Agent}, DefaultSeam: target.Self, Armable: true, New: func(KindDeps) tracing.TracePlugin { return exec.NewKind(nil) }})
}
