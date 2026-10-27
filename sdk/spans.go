package telltale

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/flanksource/telltale/sdk/protocol"
)

type Span struct {
	TraceID    string            `json:"traceId"`
	SpanID     string            `json:"spanId"`
	ParentID   string            `json:"parentId,omitempty"`
	Name       string            `json:"name"`
	Kind       string            `json:"kind"`
	StartedAt  time.Time         `json:"startedAt"`
	DurationMs float64           `json:"durationMs"`
	Status     string            `json:"status"`
	Attrs      map[string]any    `json:"attrs,omitempty"`
	Resource   map[string]string `json:"resource,omitempty"`
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
	span := Span{TraceID: parent.TraceID, ParentID: parent.SpanID, SpanID: spanID(8), Name: name, Kind: "internal", StartedAt: time.Now(), Attrs: values}
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
				span.Attrs["error"] = err.Error()
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
