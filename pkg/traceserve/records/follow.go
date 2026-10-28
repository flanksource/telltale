package records

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/telltale/pkg/traces/read"
)

const heartbeatInterval = 15 * time.Second

func follow(w http.ResponseWriter, r *http.Request, refs []read.Ref, q read.Query, format string) {
	ctx, cancel := context.WithCancel(r.Context())
	rows := make(chan query.Row)
	finished := make(chan struct{})
	var followErr error
	go func() {
		followErr = read.FollowSelection(ctx, refs, q, func(row query.Row) error {
			select {
			case rows <- row:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		close(finished)
	}()
	defer func() { cancel(); <-finished }()
	contentType := "text/event-stream"
	if format == "ndjson" {
		contentType = "application/x-ndjson"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	controller := http.NewResponseController(w)
	if err := controller.Flush(); err != nil {
		return
	}
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case row := <-rows:
			if err := writeRow(w, row, format); err != nil {
				return
			}
		case <-finished:
			if ctx.Err() != nil {
				return
			}
			if format == "ndjson" {
				if followErr != nil {
					panic(http.ErrAbortHandler)
				}
				return
			}
			if followErr != nil {
				payload, marshalErr := json.Marshal(map[string]string{"error": followErr.Error()})
				if marshalErr != nil {
					panic(http.ErrAbortHandler)
				}
				_, _ = fmt.Fprintf(w, "event: error\ndata: %s\n\n", payload)
			} else {
				_, _ = fmt.Fprint(w, "event: end\ndata: {}\n\n")
			}
			_ = controller.Flush()
			return
		case <-ticker.C:
			if format == "ndjson" {
				continue
			}
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
		}
		if err := controller.Flush(); err != nil {
			return
		}
	}
}

func writeRow(w http.ResponseWriter, row query.Row, format string) error {
	data, err := json.Marshal(row)
	if err != nil {
		return err
	}
	if format == "ndjson" {
		_, err = fmt.Fprintf(w, "%s\n", data)
		return err
	}
	seq, ok := row["seq"].(int64)
	if !ok {
		return fmt.Errorf("expected int64 seq, got %T", row["seq"])
	}
	_, err = fmt.Fprintf(w, "id: %d\ndata: %s\n\n", seq, data)
	return err
}
