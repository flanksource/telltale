package traces

import (
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/entity"
	"github.com/flanksource/telltale/pkg/tracing/target"
	"github.com/flanksource/telltale/pkg/tracing/task"
)

// The clicky kinds read the probes github.com/flanksource/clicky/telltale
// registers in a clicky process; telltale itself registers neither.
func init() {
	MustRegister(Registration{
		Name: entity.Kind, Title: "Entity operations", Summary: "Record a clicky agent's entity operations",
		Description: "Records the clicky entity operations (list, get, create, actions) an agent runs over the CLI, HTTP or MCP, with masked parameters and never their results.",
		Examples:    []Example{{Args: "--pid 1234 --entity widget --errors-only --duration 1m", Says: "record failed widget operations in process 1234"}},
		Seams:       []target.Seam{target.Agent}, DefaultSeam: target.Agent, Armable: true,
		New: func(KindDeps) tracing.TracePlugin { return entity.NewKind(nil) },
	})
	MustRegister(Registration{
		Name: task.Kind, Title: "Tasks", Summary: "Record a clicky agent's task transitions and progress",
		Description: "Records the clicky tasks an agent already runs as it arms, then each status transition and throttled progress. Task logs and output are left to the logs and exec kinds.",
		Examples:    []Example{{Args: "--pid 1234 --duration 1m", Says: "record the tasks of process 1234"}},
		Seams:       []target.Seam{target.Agent}, DefaultSeam: target.Agent, Armable: true,
		New: func(KindDeps) tracing.TracePlugin { return task.NewKind(nil) },
	})
}
