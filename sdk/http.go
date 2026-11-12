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
	wrap    func(http.RoundTripper) http.RoundTripper
}

func (t httpTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if !Armed("http") {
		return t.next.RoundTrip(request)
	}
	return t.capture.RoundTrip(request)
}

// Unwrap implements commons http.TransportWrapper so clients can reconfigure
// the transport beneath the capture layer.
func (t httpTransport) Unwrap() http.RoundTripper { return t.next }

// Rewrap implements commons http.TransportWrapper: it captures through the same
// collector around a reconfigured inner transport.
func (t httpTransport) Rewrap(inner http.RoundTripper) http.RoundTripper {
	if inner == nil {
		panic("telltale: HTTP transport must not be nil")
	}
	return httpTransport{next: inner, capture: t.wrap(inner), wrap: t.wrap}
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
	wrap := collector.Middleware()
	return httpTransport{next: next, capture: wrap(next), wrap: wrap}
}

// InstrumentDefaultTransport wraps the default HTTP transport at application startup.
func InstrumentDefaultTransport() { http.DefaultTransport = Transport(http.DefaultTransport) }
