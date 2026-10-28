package traces

import (
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/logs"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

func init() {
	MustRegister(Registration{Name: logs.Kind, Title: "Logs", Summary: "Record application logs", Seams: []target.Seam{target.Self, target.Agent, target.Process}, DefaultSeam: target.Self, Armable: true, New: func(KindDeps) tracing.TracePlugin { return logs.NewKind(nil) }})
}
