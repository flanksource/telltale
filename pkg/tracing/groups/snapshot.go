package groups

import (
	"context"
	clickyprocess "github.com/flanksource/clicky/process"
	"github.com/flanksource/telltale/pkg/tracing/process"
	"maps"
	"slices"
)

// Snapshot holds attribution and measurements from the same unfiltered tick.
type Snapshot struct {
	Frame       Frame                              `json:"frame"`
	Processes   []ProcessSample                    `json:"processes"`
	Observed    []process.Observed                 `json:"-"`
	Descriptors map[int][]clickyprocess.Descriptor `json:"-"`
	IO          map[int]clickyprocess.Process      `json:"-"`
}

func (t *Top) Snapshot(ctx context.Context) (Snapshot, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	frame, err := t.collect(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Frame: frame, Processes: slices.Clone(t.processes), Observed: slices.Clone(t.observed), Descriptors: maps.Clone(t.held), IO: maps.Clone(t.io)}, nil
}

func (t *Top) Tick(ctx context.Context) (Frame, error) {
	snapshot, err := t.Snapshot(ctx)
	if err != nil {
		return Frame{}, err
	}
	shown := snapshot.Frame.Groups[:0]
	kept := map[string]bool{}
	for _, group := range snapshot.Frame.Groups {
		if group.Kind == KindOther && !t.options.All && !group.Total.Hogs.Pinned(snapshot.Frame.SampledAt) {
			continue
		}
		if len(t.options.Groups) > 0 && !slices.Contains(t.options.Groups, group.Name) && !kept[group.ParentKey] {
			continue
		}
		kept[group.Key] = true
		shown = append(shown, group)
	}
	snapshot.Frame.Groups = shown
	return snapshot.Frame, nil
}
