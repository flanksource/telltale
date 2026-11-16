package jfr

import (
	"strings"

	"github.com/flanksource/clicky"
	"github.com/flanksource/clicky/api"
	"github.com/flanksource/telltale/pkg/tracing"
)

func (r Sample) Pretty() api.Text {
	return clicky.Text(r.Event, "text-blue-500 font-bold").Space().Append(r.Thread, "text-muted").
		Space().Add(tracing.PrettySampleValue(r.Value, r.Unit)).Space().Append(r.Frame, "font-mono")
}

func (r Sample) PrettyFull() api.Textable {
	return r.Pretty().NewLine().Add(clicky.CodeBlock("text", strings.ReplaceAll(r.Stack, ";", "\n")))
}
