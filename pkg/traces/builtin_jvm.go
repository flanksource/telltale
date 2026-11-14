package traces

import (
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/jvmtrace"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

func init() {
	MustRegister(Registration{Name: jvmtrace.NotificationsKind, Title: "JMX notifications / GC", Summary: "Subscribe to JVM notifications", Description: "Record notifications from an MBean or ObjectName pattern, including decoded garbage collections and dropped-event counts.", Seams: []target.Seam{target.Agent}, DefaultSeam: target.Agent, Armable: true, Checkpoint: jvmtrace.CheckpointParams{}, New: func(KindDeps) tracing.TracePlugin { return jvmtrace.NewNotificationKind(nil) }})
	MustRegister(Registration{Name: jvmtrace.ExpressionKind, Title: "Periodic OGNL", Summary: "Sample an expression in a selected application loader", Description: "Evaluate an OGNL expression periodically and record bounded object graphs.", Seams: []target.Seam{target.Agent}, DefaultSeam: target.Agent, Armable: true, Checkpoint: jvmtrace.CheckpointParams{}, New: func(KindDeps) tracing.TracePlugin { return jvmtrace.NewExpressionKind(nil) }})
	MustRegister(Registration{Name: jvmtrace.RedefineKind, Title: "JVM redefinition", Summary: "Replace Java code for a capture and restore its original bytes", Description: "Compiles replacement source against a selected live class, applies it under a lease, and restores it when the capture ends.", Seams: []target.Seam{target.Agent}, DefaultSeam: target.Agent, Armable: true, Checkpoint: jvmtrace.CheckpointParams{}, New: func(KindDeps) tracing.TracePlugin { return jvmtrace.NewRedefineKind(nil) }})
	MustRegister(Registration{Name: jvmtrace.Kind, Title: "JVM methods", Summary: "Watch method values or trace nested calls", Description: "Instrument selected methods in a connected JVM, record values and call timings, and restore the original bytecode when the capture ends.", Seams: []target.Seam{target.Agent}, DefaultSeam: target.Agent, Armable: true, Checkpoint: jvmtrace.CheckpointParams{}, New: func(KindDeps) tracing.TracePlugin { return jvmtrace.NewKind(nil) }})
}
