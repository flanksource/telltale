package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/flanksource/clicky/entity"
	"github.com/flanksource/telltale/internal/livestream"
	"github.com/flanksource/telltale/internal/rules"
)

const RulesMount = "/api/rules"

type rulesHandler struct{ service *rules.Service }

var errRulesQuery = errors.New("invalid rules query")

func (h rulesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Query().Get("follow") == "1" {
		h.follow(w, r)
		return
	}
	if h.service == nil {
		if r.URL.Path == RulesMount+"/status" {
			writeRulesJSON(w, rules.Status{Incidents: []rules.Incident{}})
			return
		}
		http.Error(w, "resource rules are not enabled; start serve with --rules", http.StatusServiceUnavailable)
		return
	}
	var body any
	var err error
	switch r.URL.Path {
	case RulesMount:
		body = map[string]any{"config": h.service.Program().Config, "revision": h.service.Program().Revision, "bindings": rules.Bindings(), "schema": rules.Schema()}
	case RulesMount + "/status":
		body = h.service.Status()
	case RulesMount + "/preview":
		body, err = h.service.Preview(r.Context())
	case RulesMount + "/events":
		body, err = h.events(r)
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, errRulesQuery) {
			status = http.StatusBadRequest
		}
		if errors.Is(err, rules.ErrNotReady) {
			status = http.StatusServiceUnavailable
		}
		http.Error(w, err.Error(), status)
		return
	}
	writeRulesJSON(w, body)
}
func writeRulesJSON(w http.ResponseWriter, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(append(data, '\n')); err != nil {
		slog.Debug("Resource rules response delivery failed", "error", err)
	}
}
func (h rulesHandler) events(r *http.Request) ([]rules.Event, error) {
	filter, err := ruleEventFilter(r)
	if err != nil {
		return nil, err
	}
	events, err := h.service.Events(r.Context())
	if err != nil {
		return nil, err
	}
	filtered := []rules.Event{}
	for _, event := range events {
		if filter.Match(event) {
			filtered = append(filtered, event)
		}
	}
	if len(filtered) > filter.Limit {
		filtered = filtered[len(filtered)-filter.Limit:]
	}
	return filtered, nil
}

func ruleEventFilter(r *http.Request) (rules.EventFilter, error) {
	limit := 100
	if text := r.URL.Query().Get("limit"); text != "" {
		n, err := strconv.Atoi(text)
		if err != nil || n < 1 || n > 10000 {
			return rules.EventFilter{}, fmt.Errorf("%w: limit must be 1..10000", errRulesQuery)
		}
		limit = n
	}
	var since time.Time
	if text := r.URL.Query().Get("since"); text != "" {
		value, err := time.Parse(time.RFC3339, text)
		if err != nil {
			return rules.EventFilter{}, fmt.Errorf("%w: since: %w", errRulesQuery, err)
		}
		since = value
	}
	params := r.URL.Query()
	rule, group, incident := params.Get("rule"), params.Get("group"), params.Get("incident")
	return rules.EventFilter{Limit: limit, Match: func(event rules.Event) bool {
		return (rule == "" || event.Incident.Rule == rule) && (group == "" || event.Incident.Group == group) &&
			(incident == "" || event.Incident.ID == incident) && (since.IsZero() || !event.At.Before(since))
	}}, nil
}

func (h rulesHandler) follow(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case RulesMount + "/status", RulesMount + "/preview":
		livestream.Serve(w, r, h.followSnapshots(r.URL.Path == RulesMount+"/preview"))
	case RulesMount + "/events":
		if h.service == nil {
			http.Error(w, "resource rules are not enabled", http.StatusServiceUnavailable)
			return
		}
		filter, err := ruleEventFilter(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		livestream.Serve(w, r, func(ctx context.Context, send entity.StreamSend) error {
			return h.service.FollowEvents(ctx, filter, r.Header.Get("Last-Event-ID"), func(cursor string, update rules.EventUpdate) error {
				return send(entity.StreamEvent{ID: cursor, Data: update})
			})
		})
	default:
		http.NotFound(w, r)
	}
}

func (h rulesHandler) followSnapshots(preview bool) livestream.Follow {
	return func(ctx context.Context, send entity.StreamSend) error {
		if h.service == nil {
			if preview {
				return fmt.Errorf("resource rules are not enabled")
			}
			return send(entity.StreamEvent{Data: rules.Status{Incidents: []rules.Incident{}}})
		}
		wake, unsubscribe := h.service.Changes()
		defer unsubscribe()
		for {
			var body any = h.service.Status()
			var err error
			if preview {
				body, err = h.service.Preview(ctx)
			}
			if err == nil {
				if err := send(entity.StreamEvent{Data: body}); err != nil {
					return err
				}
			} else if !errors.Is(err, rules.ErrNotReady) {
				return err
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-wake:
			}
		}
	}
}
