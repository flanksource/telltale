package prometheus

import (
	"time"

	"github.com/flanksource/clicky"
	"github.com/flanksource/clicky/api"
	"github.com/flanksource/telltale/pkg/tracing"
)

func (r Scrape) Pretty() api.Text {
	t := clicky.Text(r.Health, tracing.SeverityStyle(r.Health)).Space().Append(r.Endpoint, "text-blue-500").
		Space().Add(api.HumanNumber(r.Samples)).Append(" samples", "text-muted").Space().
		Add(api.Human(time.Duration(r.DurationMs*float64(time.Millisecond)), "text-muted"))
	if r.Error != "" {
		t = t.Space().Append(r.Error, "text-red-500")
	}
	return t
}

func (r Scrape) PrettyFull() api.Textable { return r.Pretty() }
