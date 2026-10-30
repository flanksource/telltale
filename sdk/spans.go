package telltale

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/flanksource/telltale/sdk/protocol"
)

// Span is the one record every span source stores: SDK and agent spans,
// spans an OTLP receiver accepted, and span documents read from OpenSearch.
type Span struct {
	Key        string         `json:"key" filter:"exact"`
	TraceID    string         `json:"traceId" filter:"exact"`
	SpanID     string         `json:"spanId" filter:"exact"`
	ParentID   string         `json:"parentId,omitempty" filter:"exact"`
	Service    string         `json:"service" filter:"terms"`
	Operation  string         `json:"operation" filter:"terms"`
	Kind       string         `json:"kind,omitempty"`
	StartedAt  time.Time      `json:"startedAt"`
	DurationMs float64        `json:"durationMs" sort:"durationMs"`
	Status     string         `json:"status" filter:"terms"`
	Attributes map[string]any `json:"attributes,omitempty" pretty:"hide"`
	Resource   map[string]any `json:"resource,omitempty" pretty:"hide"`
	Source     SpanSource     `json:"source,omitzero" pretty:"hide"`
	// Native and Digest are the redacted OTLP request and its canonical
	// digest, kept only for spans an OTLP receiver accepted.
	Native string `json:"native,omitempty" filter:"-" pretty:"hide"`
	Digest string `json:"digest,omitempty" filter:"-" pretty:"hide"`
}

// SpanSource says where a span was recorded; an OpenSearch span also names
// the index and document it was read from.
type SpanSource struct {
	Type  string `json:"type"`
	Index string `json:"index,omitempty"`
	ID    string `json:"id,omitempty"`
}

const (
	SpanSourceSDK        = "sdk"
	SpanSourceOTLP       = "otlp"
	SpanSourceOpenSearch = "opensearch"
)

// NewSpanKey is the key a span is stored under: its trace ID and span ID.
func NewSpanKey(traceID, spanID string) (string, error) {
	if traceID == "" || spanID == "" {
		return "", fmt.Errorf("a span requires a trace ID and span ID, got %q and %q", traceID, spanID)
	}
	return traceID + "/" + spanID, nil
}

// NormalizeSpanStatus maps the status spellings of every span source (SDK,
// OTLP code names and numbers, Jaeger error tags) to ok, error or unset.
func NormalizeSpanStatus(status string) (string, error) {
	switch strings.TrimPrefix(strings.ToLower(strings.TrimSpace(status)), "status_code_") {
	case "", "unset", "0", "false":
		return "unset", nil
	case "ok", "1":
		return "ok", nil
	case "error", "2", "true":
		return "error", nil
	}
	return "", fmt.Errorf("unknown span status %q: expected ok, error or unset", status)
}

// Normalize keys the span, canonicalizes its status and, when it has none,
// takes its service from the resource's service.name.
func (s *Span) Normalize() error {
	key, err := NewSpanKey(s.TraceID, s.SpanID)
	if err != nil {
		return err
	}
	status, err := NormalizeSpanStatus(s.Status)
	if err != nil {
		return err
	}
	if value, found := s.Resource["service.name"]; found && s.Service == "" {
		service, ok := value.(string)
		if !ok {
			return fmt.Errorf("span %s: resource service.name must be a string, got %T", key, value)
		}
		s.Service = service
	}
	s.Key, s.Status = key, status
	return nil
}

type spanProbe struct{}

var spanInstaller struct {
	sync.RWMutex
	install func() (func() error, error)
}

// RegisterSpanInstaller installs the optional span instrumentation adapter.
func RegisterSpanInstaller(install func() (func() error, error)) {
	spanInstaller.Lock()
	defer spanInstaller.Unlock()
	if install == nil || spanInstaller.install != nil {
		panic("duplicate or nil span installer")
	}
	spanInstaller.install = install
}

func init()                                { RegisterProbe(spanProbe{}) }
func (spanProbe) Spec() protocol.ProbeSpec { return protocol.ProbeSpec{Name: "spans", Title: "Spans"} }
func (spanProbe) Install() (func() error, error) {
	spanInstaller.RLock()
	defer spanInstaller.RUnlock()
	if spanInstaller.install == nil {
		return func() error { return nil }, nil
	}
	return spanInstaller.install()
}
func (spanProbe) Arm(context.Context, json.RawMessage, Sink) (func() error, error) {
	return func() error { return nil }, nil
}

type spanContextKey struct{}

// StartSpan records a manually instrumented span while the spans probe is armed.
func StartSpan(ctx context.Context, name string, attrs ...any) (context.Context, func(error)) {
	if len(attrs)%2 != 0 {
		log.Print("span attributes must be key-value pairs")
		return ctx, func(error) {}
	}
	for i := 0; i < len(attrs); i += 2 {
		key, ok := attrs[i].(string)
		if !ok || key == "" {
			log.Printf("span attribute key at index %d must be a non-empty string", i)
			return ctx, func(error) {}
		}
	}
	if !Armed("spans") {
		return ctx, func(error) {}
	}
	values := make(map[string]any, len(attrs)/2)
	for i := 0; i < len(attrs); i += 2 {
		values[attrs[i].(string)] = attrs[i+1]
	}
	parent, _ := ctx.Value(spanContextKey{}).(Span)
	span := Span{TraceID: parent.TraceID, ParentID: parent.SpanID, SpanID: spanID(8), Operation: name, Kind: "internal", StartedAt: time.Now(), Attributes: values, Source: SpanSource{Type: SpanSourceSDK}}
	if span.TraceID == "" {
		span.TraceID = spanID(16)
	}
	ctx = context.WithValue(ctx, spanContextKey{}, span)
	var once sync.Once
	return ctx, func(err error) {
		once.Do(func() {
			span.DurationMs = float64(time.Since(span.StartedAt)) / float64(time.Millisecond)
			span.Status = "ok"
			if err != nil {
				span.Status = "error"
				span.Attributes["error"] = err.Error()
			}
			if err := span.Normalize(); err != nil {
				log.Printf("span %q: %v", name, err)
				return
			}
			Record(ctx, "spans", span)
		})
	}
}

func spanID(size int) string {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		panic(fmt.Errorf("generate span id: %w", err))
	}
	return hex.EncodeToString(value)
}
