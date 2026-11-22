package livestream

import (
	"context"
	"net/http"
	"time"

	"github.com/flanksource/clicky/entity"
	"github.com/flanksource/clicky/sse"
)

type Follow func(context.Context, entity.StreamSend) error

// Serve keeps transport heartbeats independent of the producer's sampling or commit cadence.
func Serve(w http.ResponseWriter, r *http.Request, follow Follow) {
	writer := sse.NewWriter(w)
	if err := writer.Ping(); err != nil {
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	events := make(chan entity.StreamEvent)
	done := make(chan error, 1)
	go func() {
		done <- follow(ctx, func(event entity.StreamEvent) error {
			select {
			case events <- event:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		close(done)
	}()
	defer func() { cancel(); <-done }()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-events:
			if err := writer.Send(event); err != nil {
				return
			}
		case err := <-done:
			if ctx.Err() == nil {
				if err != nil {
					_ = writer.Send(entity.StreamEvent{Name: "error", Data: map[string]string{"error": err.Error()}})
				} else {
					_ = writer.Send(entity.StreamEvent{Name: "end", Data: struct{}{}})
				}
			}
			return
		case <-heartbeat.C:
			if err := writer.Ping(); err != nil {
				return
			}
		}
	}
}
