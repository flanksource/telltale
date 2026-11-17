package traces

import (
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/httptop"
	"github.com/flanksource/telltale/pkg/tracing/jms"
	"github.com/flanksource/telltale/pkg/tracing/jmstop"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

func init() {
	MustRegister(Registration{Name: jmstop.Kind, Title: "JMS top", Summary: "Map JMS receive handlers and their activity", Description: "Retains discovered destination-to-handler mappings, including idle handlers.", Seams: []target.Seam{target.Agent}, DefaultSeam: target.Agent, Armable: true, Checkpoint: map[string]any{}, New: func(KindDeps) tracing.TracePlugin { return jmstop.NewKind(nil) }})
	MustRegister(Registration{
		Name: httptop.Kind, Title: "HTTP top", Summary: "Sample active incoming and outgoing HTTP exchanges",
		Description: "Samples requests observed by an attached JVM, including Spring and asynchronous response bodies. Each complete snapshot replaces the previous view; an empty snapshot clears it.",
		Seams:       []target.Seam{target.Agent}, DefaultSeam: target.Agent, Armable: true, Checkpoint: map[string]any{},
		Examples: []Example{{Args: "--agent java/checkout --allow-mutation --direction both --poll 5s --top 50", Says: "watch the longest active JVM HTTP exchanges"}},
		New:      func(KindDeps) tracing.TracePlugin { return httptop.NewKind(nil) },
	})
	MustRegister(Registration{
		Name: jms.Kind, Title: "JMS", Summary: "Record incoming and outgoing JVM messaging operations",
		Description: "Records actual sent and received messages, including handler outcomes and failed sends with a known message. Empty receive polls are excluded. Message bodies are disabled by default.",
		Seams:       []target.Seam{target.Agent}, DefaultSeam: target.Agent, Armable: true, Checkpoint: map[string]any{},
		Examples: []Example{{Args: "--agent java/checkout --allow-mutation --direction both", Says: "record JVM JMS operations without reading message bodies"}},
		New:      func(KindDeps) tracing.TracePlugin { return jms.NewKind(nil) },
	})
}
