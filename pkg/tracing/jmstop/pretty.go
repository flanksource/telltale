package jmstop

import (
	"fmt"
	"time"

	"github.com/flanksource/clicky"
	"github.com/flanksource/clicky/api"
)

func (s Sample) Pretty() api.Text {
	return api.Text{}.Append("JMS handler", "font-bold").Space().Append(s.Destination).Space().Append(s.Handler).Space().Append(s.State, "text-muted").Space().Append(fmt.Sprintf("%d active", s.ActiveCount)).Space().Add(clicky.Human(time.Duration(s.ElapsedMs * float64(time.Millisecond))))
}

func (s Sample) PrettyFull() api.Textable {
	return s.Pretty().NewLine().Add(clicky.Map(map[string]any{"source": s.Source, "handlerId": s.HandlerID, "subscription": s.Subscription, "sampledAt": s.SampledAt, "resource": s.Resource}))
}

func (s Sample) RowDetail() api.Textable { return s.PrettyFull() }

func (Sample) Columns() []api.ColumnDef {
	return []api.ColumnDef{
		api.Column("destination").Label("Destination").MaxWidth(16).Build(),
		api.Column("handler").Label("Handler").MaxWidth(32).Build(),
		api.Column("state").Label("State").Build(),
		api.Column("activeCount").Label("Active").Format(api.FormatInteger).Build(),
		api.Column("elapsedMs").Label("Elapsed").Type("duration").Unit("ms").Build(),
		api.Column("subscription").Label("Subscription").DefaultHidden().Build(),
		api.Column("source").Label("Source").DefaultHidden().Build(),
		api.Column("handlerId").Label("Handler ID").DefaultHidden().Build(),
		api.Column("sampledAt").Label("Sampled at").DefaultHidden().Build(),
		api.Column("resource").Label("Resource").DefaultHidden().Build(),
	}
}

func (s Sample) Row() map[string]any {
	return map[string]any{"destination": s.Destination, "handler": s.Handler, "state": s.State, "activeCount": api.Text{Content: fmt.Sprint(s.ActiveCount)}, "elapsedMs": s.ElapsedMs, "subscription": s.Subscription, "source": s.Source, "handlerId": s.HandlerID, "sampledAt": s.SampledAt, "resource": s.Resource}
}
