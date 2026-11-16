package entity

import (
	"maps"
	"slices"
	"time"

	"github.com/flanksource/clicky"
	"github.com/flanksource/clicky/api"
)

func (r Operation) Pretty() api.Text {
	t := clicky.Text(r.Entity, "font-bold").Space().Append(r.Verb, "text-blue-500 font-bold")
	if r.TargetID != "" {
		t = t.Space().Append(r.TargetID)
	}
	if r.Surface != "" {
		t = t.Space().Append(r.Surface, "text-muted")
	}
	t = t.Space().Add(api.Human(time.Duration(r.DurationMs*float64(time.Millisecond)), "text-muted"))
	if r.Error != "" {
		t = t.Space().Append(r.Error, "text-red-500")
	}
	return t
}

func (r Operation) PrettyFull() api.Textable {
	t := r.Pretty()
	for _, key := range slices.Sorted(maps.Keys(r.Parameters)) {
		t = t.NewLine().Append(key, "text-muted").Append(": ").Append(r.Parameters[key])
	}
	return t
}
