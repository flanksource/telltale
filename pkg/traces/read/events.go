package read

import "github.com/flanksource/telltale/pkg/tracing"

// EventRefs selects captures for the combined event view.
func EventRefs(refs []Ref) []Ref {
	events := make([]Ref, 0, len(refs))
	for _, ref := range refs {
		owner, _ := ref.Runtime.Kinds.ResultOf(ref.Kind)
		plugin, _ := ref.Runtime.Kinds.Get(owner)
		if tracing.TopOf(plugin) != nil {
			continue
		}
		plugin, _ = ref.Runtime.Kinds.Get(ref.Kind)
		if tracing.TopOf(plugin) == nil {
			events = append(events, ref)
		}
	}
	return events
}
