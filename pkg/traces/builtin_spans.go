package traces

import (
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/spans"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

func init() {
	MustRegister(Registration{
		Name: spans.Kind, Title: "Spans", Summary: "Record application spans or read them from OpenSearch",
		Description: "Records SDK spans in telltale itself or an instrumented agent; the host seam reads the span documents of an OpenTelemetry connection's OpenSearch index over a window, or follows it.",
		Examples: []Example{
			{Args: "--pid 1234 --duration 1m", Says: "record spans from the agent in process 1234"},
			{Args: "--seam host --opensearch-connection traces --opensearch-from now-1h", Says: "import the last hour of spans from OpenSearch"},
		},
		Seams: []target.Seam{target.Self, target.Agent, target.Host}, DefaultSeam: target.Self, Armable: true,
		New: func(KindDeps) tracing.TracePlugin { return spans.NewKind(nil) },
	})
}
