package traces

import (
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/spans"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

func init() {
	MustRegister(Registration{Name: spans.Kind, Title: "Spans", Summary: "Record application spans", Seams: []target.Seam{target.Self, target.Agent}, DefaultSeam: target.Self, Armable: true, New: func(KindDeps) tracing.TracePlugin { return spans.NewKind(nil) }})
}
