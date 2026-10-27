package traces

import (
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/llm"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

func init() {
	MustRegister(Registration{Name: llm.Kind, Title: "LLM calls", Summary: "Record observed provider calls", Description: "Records Captain provider calls in telltale itself or an instrumented agent. Prompts and responses are excluded unless explicitly requested.", Examples: []Example{{Args: "--pid 1234 --include-prompts --duration 1m", Says: "record calls and prompts from the agent in process 1234"}}, Seams: []target.Seam{target.Self, target.Agent}, DefaultSeam: target.Self, Armable: true, New: func(KindDeps) tracing.TracePlugin { return llm.NewKind(nil) }})
}
