package server

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/flanksource/clicky/entity"
	"github.com/flanksource/telltale/internal/livestream"
)

func (h *TopHandler) follow(w http.ResponseWriter, r *http.Request, choice topChoice, project topRead) {
	minimum := int64(1000)
	if r.URL.Path == TopFacetsPath {
		minimum = 5000
	}
	if requested := r.URL.Query().Get("everyMs"); requested != "" {
		value, err := strconv.ParseInt(requested, 10, 64)
		if err != nil || value <= 0 || value > 60000 {
			http.Error(w, "everyMs must be between 1 and 60000", http.StatusBadRequest)
			return
		}
		minimum = max(minimum, value)
	}
	livestream.Serve(w, r, func(ctx context.Context, send entity.StreamSend) error {
		var next time.Time
		var last int64
		return h.updates.Follow(ctx, choice, func(ctx context.Context) (TopFrame, time.Duration, error) {
			value, err := h.latest(ctx, choice, func(live *liveTop, polling TopPolling) (any, error) {
				return TopFrame{Frame: live.frame, Polling: polling}, nil
			})
			if err != nil {
				return TopFrame{}, 0, err
			}
			frame := value.(TopFrame)
			return frame, time.Duration(frame.Polling.MinimumIntervalMS) * time.Millisecond, nil
		}, func(frame TopFrame) error {
			if frame.Tick == last || time.Now().Before(next) {
				return nil
			}
			value, tick, err := h.projectTop(ctx, choice, project, frame.Polling)
			if err != nil {
				return err
			}
			last = tick
			next = time.Now().Add(time.Duration(minimum) * time.Millisecond)
			return send(entity.StreamEvent{Data: value})
		})
	})
}

func (h *TopHandler) projectTop(ctx context.Context, choice topChoice, read topRead, polling TopPolling) (any, int64, error) {
	select {
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	case h.gate <- struct{}{}:
	}
	defer func() { <-h.gate }()
	live := h.tops[choice]
	if live == nil {
		return nil, 0, fmt.Errorf("host Top scope has no sample")
	}
	value, err := read(live, polling)
	return value, live.frame.Tick, err
}
