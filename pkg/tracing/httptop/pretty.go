package httptop

import (
	"time"

	"github.com/flanksource/clicky/api"
	tracehttp "github.com/flanksource/telltale/pkg/tracing/http"
)

func (s Sample) Pretty() api.Text {
	target := s.URL
	if target == "" {
		target = s.Route
	}
	text := api.Text{}.Append(s.Method, "font-bold").Space().Append(target).
		Space().Add(api.Human(time.Duration(s.ElapsedMs*float64(time.Millisecond)), "text-muted"))
	if arrow := tracehttp.DirectionArrow(s.Direction); arrow != "" {
		text = text.Prefix(arrow + " ")
	}
	return text
}
