package goprof

import (
	"fmt"

	"github.com/flanksource/clicky"
	"github.com/flanksource/clicky/api"
	"github.com/flanksource/telltale/pkg/tracing"
)

func (r Sample) Pretty() api.Text {
	t := clicky.Text(fmt.Sprintf("pid %d", r.PID), "text-muted").Space().Append(r.Profile, "text-blue-500 font-bold").
		Space().Append(r.SampleType, "text-muted").Space().Add(tracing.PrettySampleValue(r.Value, r.Unit))
	if len(r.Frames) > 0 {
		t = t.Space().Append(r.Frames[0].Func, "font-mono")
	}
	return t
}

func (r Sample) PrettyFull() api.Textable {
	t := r.Pretty()
	for _, frame := range r.Frames {
		t = t.NewLine().Add(frame.Pretty())
	}
	return t
}
