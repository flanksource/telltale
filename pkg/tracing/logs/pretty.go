package logs

import (
	"strings"

	"github.com/flanksource/clicky"
	"github.com/flanksource/clicky/api"
	"github.com/flanksource/telltale/pkg/tracing"
)

func (r Log) Pretty() api.Text {
	return clicky.Text(r.FirstObserved.Format("15:04:05.000"), "text-muted").Space().
		Append(strings.ToUpper(r.Severity), tracing.SeverityStyle(r.Severity)).
		Space().Append(r.Logger, "text-blue-500").Space().Append(r.Message)
}

func (r Log) PrettyFull() api.Textable {
	return r.Pretty().NewLine().Add(clicky.Map(r.Labels)).NewLine().Add(clicky.Map(r.Resource))
}
