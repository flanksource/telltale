package xesession

import (
	"github.com/flanksource/clicky"
	"github.com/flanksource/clicky/api"
	"github.com/flanksource/telltale/pkg/tracing/sqlserver/xetrace"
)

func (r EventRow) Pretty() api.Text {
	return xetrace.StreamLine(r.Event(), xetrace.StreamLineOptions{ShowDatabase: true})
}

func (r EventRow) PrettyFull() api.Textable {
	t := xetrace.StreamLine(r.Event(), xetrace.StreamLineOptions{ShowDatabase: true, Full: true})
	if r.SQL != "" {
		t = t.NewLine().Add(clicky.CodeBlock("sql", r.SQL))
	}
	if r.Deadlock != nil {
		t = t.NewLine().Add(r.Deadlock.Pretty())
	}
	for _, child := range r.Children {
		t = t.NewLine().Add(child.PrettyFull())
	}
	return t
}
