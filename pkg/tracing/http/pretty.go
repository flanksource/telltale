package http

import (
	"github.com/flanksource/clicky/api"
	"github.com/flanksource/commons/har/harpretty"
)

func (e Exchange) Pretty() api.Text {
	t := harpretty.Entry(e.Entry).Pretty()
	if e.Error != "" {
		t = t.Space().Append(e.Error, "text-red-500")
	}
	return t
}

func (e Exchange) PrettyFull() api.Textable {
	t := e.Pretty()
	if detail := harpretty.Entry(e.Entry).RowDetail(); detail != nil {
		t = t.NewLine().Add(detail)
	}
	return t
}
