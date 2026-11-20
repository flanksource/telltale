package read

import (
	"fmt"
	"github.com/flanksource/clicky"
	"github.com/flanksource/clicky/api"
)

func (r Record) Pretty() api.Text {
	return api.Text{}.Add(r.prefix()).Space().Add(r.presentation.Summary)
}

func (r Record) PrettyFull() api.Textable {
	return api.Text{}.Add(r.prefix()).Space().Add(r.presentation.Detail)
}

func (r Record) prefix() api.Textable {
	kind := r.Kind
	if r.Result != "" {
		kind += "/" + r.Result
	}
	return clicky.Text(fmt.Sprintf("%s #%d", kind, r.Seq), "text-muted")
}
