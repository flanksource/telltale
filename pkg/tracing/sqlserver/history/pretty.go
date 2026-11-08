package history

import (
	"github.com/flanksource/clicky"
	"github.com/flanksource/clicky/api"
	"github.com/flanksource/telltale/pkg/tracing/sqlserver/deadlocks"
)

func (r Row) Pretty() api.Text {
	shape := deadlocks.Shape(r.Shape)
	return clicky.Text("deadlock", "text-red-500 font-bold").Space().Append(r.Database, "text-blue-500").
		Space().Append(shape.Label(), shape.Style()).Space().Append(participantsLabel(r.Participants, r.Victims)+" participants", "text-muted").
		Space().Add(victimStatement(r))
}

func (r Row) PrettyFull() api.Textable {
	if r.DecodeError != "" {
		return r.Pretty().NewLine().Add(clicky.CodeBlock("xml", r.XML))
	}
	return r.Graph().Pretty()
}
