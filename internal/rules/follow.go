package rules

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/telltale/internal/livestream"
)

type EventFilter struct {
	Match func(Event) bool
	Limit int
}

type EventUpdate struct {
	Events []Event `json:"events"`
	Reset  bool    `json:"reset,omitempty"`
}

var endEventWindow = errors.New("end of event window")

func (s *Service) FollowEvents(ctx context.Context, filter EventFilter, cursor string, emit func(string, EventUpdate) error) error {
	j, ok := s.options.Journal.(*journal)
	if !ok {
		return fmt.Errorf("journal does not support follow")
	}
	var after int64
	var generation string
	if cursor != "" {
		id, seq, ok := strings.Cut(cursor, ":")
		last, err := strconv.ParseInt(seq, 10, 64)
		if !ok || id == "" || err != nil || last < 0 {
			return fmt.Errorf("invalid rule event cursor")
		}
		after, generation = last, id
	}
	return livestream.Commits(ctx, j.backend, "monitor", func(meta recordstore.Meta) error {
		if generation != "" && (generation != meta.Generation || after > meta.HighSeq || after < meta.LowSeq-1) {
			return fmt.Errorf("rule event cursor expired; reload the event history")
		}
		update := EventUpdate{Events: []Event{}, Reset: generation == ""}
		if meta.HighSeq != after {
			err := j.backend.Scan(ctx, "monitor", after, func(seq int64, row recordstore.Row) error {
				if seq > meta.HighSeq {
					return endEventWindow
				}
				event, err := readEvent(row)
				if err != nil {
					return err
				}
				if filter.Match(event) {
					update.Events = append(update.Events, event)
					if update.Reset && len(update.Events) > 2*filter.Limit {
						copy(update.Events, update.Events[len(update.Events)-filter.Limit:])
						clear(update.Events[filter.Limit:])
						update.Events = update.Events[:filter.Limit]
					}
				}
				return nil
			})
			if err != nil && !errors.Is(err, endEventWindow) {
				return err
			}
		}
		if update.Reset && len(update.Events) > filter.Limit {
			update.Events = slices.Clone(update.Events[len(update.Events)-filter.Limit:])
		}
		if !update.Reset && after == meta.HighSeq {
			return nil
		}
		after, generation = meta.HighSeq, meta.Generation
		return emit(generation+":"+strconv.FormatInt(after, 10), update)
	})
}
