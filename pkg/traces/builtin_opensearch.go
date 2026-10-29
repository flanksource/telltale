package traces

import (
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/opensearch"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

func init() {
	MustRegister(Registration{Name: "opensearch", Title: "OpenSearch traces", Summary: "Read stored OpenTelemetry spans imported from OpenSearch", Seams: []target.Seam{target.Process}, DefaultSeam: target.Process, New: func(KindDeps) tracing.TracePlugin { return opensearch.Kind() }})
}
