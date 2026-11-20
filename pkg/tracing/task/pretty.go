package task

import (
	"fmt"

	"github.com/flanksource/clicky"
	"github.com/flanksource/clicky/api"
)

var statusStyles = map[string]string{"success": "text-green-500", "failed": "text-red-500", "canceled": "text-yellow-500", "running": "text-blue-500"}

func (r Snapshot) Pretty() api.Text {
	t := clicky.Text(r.Phase, "text-muted").Space().Append(r.Name, "font-bold").Space().Append(r.Status, statusStyles[r.Status]+" font-bold")
	if r.Group != "" {
		t = t.Space().Append(r.Group, "text-muted")
	}
	if r.MaxValue > 0 {
		t = t.Space().Append(fmt.Sprintf("%d/%d", r.Progress, r.MaxValue), "text-blue-500")
	}
	if r.Error != "" {
		t = t.Space().Append(r.Error, "text-red-500")
	}
	return t
}

func (r Snapshot) PrettyFull() api.Textable {
	t := r.Pretty()
	if r.Message != "" {
		t = t.NewLine().Append(r.Message, "text-muted")
	}
	return t
}
