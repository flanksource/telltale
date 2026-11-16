package traces

import (
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/httptop"
	"github.com/flanksource/telltale/pkg/tracing/jms"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

func init() {
	MustRegister(Registration{
		Name: httptop.Kind, Title: "HTTP top", Summary: "Sample active incoming and outgoing HTTP exchanges",
		Description: "Samples requests observed by an attached JVM, including Spring and asynchronous response bodies. Each complete snapshot replaces the previous view; an empty snapshot clears it.",
		Seams:       []target.Seam{target.Agent}, DefaultSeam: target.Agent, Armable: true, Checkpoint: map[string]any{},
		Examples: []Example{{Args: "--agent java/checkout --allow-mutation --direction both --poll 5s --top 50", Says: "watch the longest active JVM HTTP exchanges"}},
		New:      func(KindDeps) tracing.TracePlugin { return httptop.NewKind(nil) },
	})
	MustRegister(Registration{
		Name: jms.Kind, Title: "JMS", Summary: "Record incoming and outgoing JVM messaging operations",
		Description: "Records sends, receive waits and message processing from an attached JVM, including Spring templates and listeners. Message bodies are disabled by default.",
		Seams:       []target.Seam{target.Agent}, DefaultSeam: target.Agent, Armable: true, Checkpoint: map[string]any{},
		Examples: []Example{{Args: "--agent java/checkout --allow-mutation --direction both", Says: "record JVM JMS operations without reading message bodies"}},
		New:      func(KindDeps) tracing.TracePlugin { return jms.NewKind(nil) },
	})
}
