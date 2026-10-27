package telltale

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/flanksource/telltale/sdk/protocol"
)

type logParams struct {
	Levels   map[string]string `json:"levels"`
	MinLevel string            `json:"minLevel"`
	Match    string            `json:"match"`
}

type logCapture struct {
	levels map[string]slog.Level
	min    slog.Level
	match  string
	sink   Sink
}

var logCaptures = struct {
	sync.RWMutex
	values map[*logCapture]struct{}
}{values: make(map[*logCapture]struct{})}

type logProbe struct{}

type LogOptions struct {
	Levels   map[string]string
	MinLevel string
	Match    string
}

var logAdapter struct {
	sync.RWMutex
	arm func(LogOptions, Sink) (func() error, error)
}

func RegisterLogAdapter(arm func(LogOptions, Sink) (func() error, error)) {
	logAdapter.Lock()
	defer logAdapter.Unlock()
	if arm == nil || logAdapter.arm != nil {
		panic("duplicate or nil log adapter")
	}
	logAdapter.arm = arm
}

func init() { RegisterProbe(logProbe{}) }
func (logProbe) Spec() protocol.ProbeSpec {
	return protocol.ProbeSpec{Name: "logs", Title: "Logs", Mutates: true, Reverts: true}
}
func (logProbe) Mutating(raw json.RawMessage) (bool, error) {
	var params logParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return false, fmt.Errorf("logs params: %w", err)
	}
	return len(params.Levels) > 0, nil
}
func (logProbe) Arm(_ context.Context, raw json.RawMessage, sink Sink) (func() error, error) {
	var params logParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, fmt.Errorf("logs params: %w", err)
	}
	capture := &logCapture{levels: make(map[string]slog.Level), min: slog.Level(-100), match: params.Match, sink: sink}
	if params.MinLevel != "" {
		if err := capture.min.UnmarshalText([]byte(params.MinLevel)); err != nil {
			return nil, fmt.Errorf("logs minLevel: %w", err)
		}
	}
	for pattern, value := range params.Levels {
		if _, err := path.Match(pattern, ""); err != nil {
			return nil, fmt.Errorf("logs level pattern %q: %w", pattern, err)
		}
		var level slog.Level
		if err := level.UnmarshalText([]byte(value)); err != nil {
			return nil, fmt.Errorf("logs level %q: %w", value, err)
		}
		capture.levels[pattern] = level
	}
	logAdapter.RLock()
	adapter := logAdapter.arm
	logAdapter.RUnlock()
	var releaseAdapter func() error
	if adapter != nil {
		var err error
		releaseAdapter, err = adapter(LogOptions{Levels: params.Levels, MinLevel: params.MinLevel, Match: params.Match}, sink)
		if err != nil {
			return nil, err
		}
		if releaseAdapter == nil {
			return nil, fmt.Errorf("log adapter returned no release")
		}
	}
	logCaptures.Lock()
	logCaptures.values[capture] = struct{}{}
	logCaptures.Unlock()
	return func() error {
		logCaptures.Lock()
		delete(logCaptures.values, capture)
		logCaptures.Unlock()
		if releaseAdapter != nil {
			return releaseAdapter()
		}
		return nil
	}, nil
}

type logHandler struct {
	next   slog.Handler
	logger string
	attrs  []slog.Attr
}

// SlogHandler records logs and permits temporary named level changes.
func SlogHandler(next slog.Handler) slog.Handler {
	if next == nil {
		panic("slog handler is nil")
	}
	return &logHandler{next: next}
}

func (h *logHandler) Enabled(ctx context.Context, level slog.Level) bool {
	if h.next.Enabled(ctx, level) {
		return true
	}
	logCaptures.RLock()
	defer logCaptures.RUnlock()
	for capture := range logCaptures.values {
		for pattern, minimum := range capture.levels {
			matched, _ := path.Match(pattern, h.logger)
			if matched && level >= minimum {
				return true
			}
		}
	}
	return false
}

func (h *logHandler) Handle(ctx context.Context, record slog.Record) error {
	if !Armed("logs") {
		return h.next.Handle(ctx, record)
	}
	logger := h.logger
	span, _ := ctx.Value(spanContextKey{}).(Span)
	labels := make(map[string]string)
	for _, attr := range h.attrs {
		labels[attr.Key] = attr.Value.String()
	}
	record.Attrs(func(attr slog.Attr) bool {
		labels[attr.Key] = attr.Value.String()
		if attr.Key == "logger" {
			logger = attr.Value.String()
		}
		return true
	})
	encoded, err := json.Marshal(labels)
	if err != nil {
		return fmt.Errorf("encode log attributes for masking: %w", err)
	}
	if err := json.Unmarshal([]byte(Mask(string(encoded))), &labels); err != nil {
		return fmt.Errorf("decode masked log attributes: %w", err)
	}
	logCaptures.RLock()
	for capture := range logCaptures.values {
		if record.Level >= capture.min && strings.Contains(record.Message, capture.match) {
			capture.sink.TryEmit(struct {
				FirstObserved time.Time         `json:"firstObserved"`
				Message       string            `json:"message"`
				Severity      string            `json:"severity"`
				Logger        string            `json:"logger,omitempty"`
				TraceID       string            `json:"traceId,omitempty"`
				Labels        map[string]string `json:"labels,omitempty"`
			}{record.Time, record.Message, record.Level.String(), logger, span.TraceID, labels})
		}
	}
	logCaptures.RUnlock()
	return h.next.Handle(ctx, record)
}

func (h *logHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	copy := &logHandler{next: h.next.WithAttrs(attrs), logger: h.logger, attrs: append(append([]slog.Attr(nil), h.attrs...), attrs...)}
	for _, attr := range attrs {
		if attr.Key == "logger" {
			copy.logger = attr.Value.String()
		}
	}
	return copy
}
func (h *logHandler) WithGroup(name string) slog.Handler {
	return &logHandler{next: h.next.WithGroup(name), logger: h.logger, attrs: h.attrs}
}
