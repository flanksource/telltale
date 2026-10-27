package telltale

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"

	"github.com/flanksource/commons/har"
	"github.com/flanksource/telltale/sdk/protocol"
)

type httpProbe struct{}

var httpObserver struct {
	sync.RWMutex
	arm func(context.Context, json.RawMessage, Sink) (func() error, error)
}

// RegisterHTTPObserver adds a source of HTTP entries to the core HTTP probe.
func RegisterHTTPObserver(arm func(context.Context, json.RawMessage, Sink) (func() error, error)) {
	if arm == nil {
		panic("telltale: nil HTTP observer")
	}
	httpObserver.Lock()
	defer httpObserver.Unlock()
	if httpObserver.arm != nil {
		panic("telltale: HTTP observer already registered")
	}
	httpObserver.arm = arm
}

func init() { RegisterProbe(httpProbe{}) }

func (httpProbe) Spec() protocol.ProbeSpec {
	return protocol.ProbeSpec{Name: "http", Title: "HTTP traffic"}
}

func (httpProbe) Arm(ctx context.Context, params json.RawMessage, sink Sink) (func() error, error) {
	httpObserver.RLock()
	arm := httpObserver.arm
	httpObserver.RUnlock()
	if arm != nil {
		return arm(ctx, params, sink)
	}
	return func() error { return nil }, nil
}

type httpTransport struct {
	next    http.RoundTripper
	capture http.RoundTripper
}

func (t httpTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if !Armed("http") {
		return t.next.RoundTrip(request)
	}
	return t.capture.RoundTrip(request)
}

// Transport captures requests made through next when the HTTP probe is armed.
func Transport(next http.RoundTripper) http.RoundTripper {
	if next == nil {
		panic("telltale: HTTP transport must not be nil")
	}
	config := har.DefaultConfig()
	config.CaptureSensitive = false
	collector := har.NewCollectorWithLifecycle(config, nil, func(entry *har.Entry) error {
		Record(context.Background(), "http", entry)
		return nil
	})
	return httpTransport{next: next, capture: collector.Middleware()(next)}
}

// InstrumentDefaultTransport wraps the default HTTP transport at application startup.
func InstrumentDefaultTransport() { http.DefaultTransport = Transport(http.DefaultTransport) }
