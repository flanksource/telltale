package read

import (
	"context"
	"errors"
	"fmt"

	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/commons-db/recordstore/sampleframe"
	"github.com/flanksource/telltale/pkg/tracing"
)

type TopUpdate struct {
	Frames []sampleframe.Frame `json:"frames"`
	Reset  bool                `json:"reset,omitempty"`
	Result *TopResult          `json:"result,omitempty"`
}

// FollowTop watches completed frame publications, including frames with no records.
func FollowTop(ctx context.Context, ref Ref, q TopQuery, emit func(TopUpdate) error) error {
	plugin, ok := ref.Runtime.Kinds.Get(ref.Kind)
	if !ok || tracing.TopOf(plugin) == nil {
		return fmt.Errorf("kind %s does not support Top", ref.Kind)
	}
	backend := ref.Runtime.Results.Backend
	stream := ref.Stream + "-frames"
	meta, err := backend.Meta(ctx, stream)
	if errors.Is(err, recordstore.ErrNotFound) && ref.ToSeq > 0 {
		return emit(TopUpdate{Frames: []sampleframe.Frame{}, Reset: true})
	}
	if err != nil {
		return err
	}
	frames := []sampleframe.Frame{}
	var last int64
	first := true
	for {
		delta := []sampleframe.Frame{}
		if meta.HighSeq > last {
			delta, err = scanFrames(ctx, ref, last, meta.HighSeq)
			if err != nil {
				return err
			}
			frames = append(frames, delta...)
			last = meta.HighSeq
		}
		if first || len(delta) > 0 {
			update := TopUpdate{Frames: delta, Reset: first}
			result, err := topAtFrames(ctx, ref, q, frames)
			if err == nil {
				update.Result = &result
			} else if !errors.Is(err, ErrNoCompleteSamples) {
				return err
			}
			if err := emit(update); err != nil {
				return err
			}
			first = false
		}
		if meta.Sealed {
			return nil
		}
		meta, err = backend.Wait(ctx, stream, last, meta.Generation)
		if err != nil {
			return err
		}
	}
}
