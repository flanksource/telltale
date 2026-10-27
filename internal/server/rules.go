package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

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
	limit := 100
	if text := r.URL.Query().Get("limit"); text != "" {
		n, err := strconv.Atoi(text)
		if err != nil || n < 1 || n > 10000 {
			return nil, fmt.Errorf("%w: limit must be 1..10000", errRulesQuery)
		}
		limit = n
	}
	var since time.Time
	if text := r.URL.Query().Get("since"); text != "" {
		value, err := time.Parse(time.RFC3339, text)
		if err != nil {
			return nil, fmt.Errorf("%w: since: %w", errRulesQuery, err)
		}
		since = value
	}
	events, err := h.service.Events(r.Context())
	if err != nil {
		return nil, err
	}
	filtered := []rules.Event{}
	for _, event := range events {
		if rule := r.URL.Query().Get("rule"); rule != "" && event.Incident.Rule != rule {
			continue
		}
		if group := r.URL.Query().Get("group"); group != "" && event.Incident.Group != group {
			continue
		}
		if incident := r.URL.Query().Get("incident"); incident != "" && event.Incident.ID != incident {
			continue
		}
		if !since.IsZero() && event.At.Before(since) {
			continue
		}
		filtered = append(filtered, event)
	}
	if len(filtered) > limit {
		filtered = filtered[len(filtered)-limit:]
	}
	return filtered, nil
}
