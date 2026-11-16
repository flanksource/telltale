package spans

import (
	"time"

	"github.com/flanksource/clicky"
	"github.com/flanksource/clicky/api"
	"github.com/flanksource/telltale/pkg/tracing"
	telltale "github.com/flanksource/telltale/sdk"
)

type spanPresentation struct{ telltale.Span }

func (r spanPresentation) Pretty() api.Text {
	return clicky.Text(r.Service, "text-blue-500 font-bold").Space().Append(r.Operation, "font-bold").
		Space().Append(r.Status, tracing.SeverityStyle(r.Status)).Space().
		Add(api.Human(time.Duration(r.DurationMs*float64(time.Millisecond)), "text-muted")).Space().Append(r.TraceID, "text-muted")
}

func (r spanPresentation) PrettyFull() api.Textable {
	return r.Pretty().NewLine().Add(clicky.Map(r.Attributes)).NewLine().Add(clicky.Map(r.Resource))
}

func (capture) Present(record telltale.Span) tracing.RecordPresentation {
	r := spanPresentation{record}
	return tracing.RecordPresentation{Summary: r.Pretty(), Detail: r.PrettyFull()}
}
