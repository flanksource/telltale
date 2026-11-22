package telltale

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"

	"github.com/flanksource/commons/har"
	"github.com/flanksource/telltale/sdk/protocol"
)

type httpProbe struct{}

type HTTPExchange struct {
	har.Entry
	Direction protocol.HTTPDirection `json:"direction"`
	Feature   string                 `json:"feature,omitempty"`
}

type httpSubscription struct {
	Sink
	selection protocol.HTTPSelection
	bodies    int64
}

var httpSubscriptions = struct {
	sync.RWMutex
	active map[*httpSubscription]struct{}
}{active: make(map[*httpSubscription]struct{})}

func (s *httpSubscription) TryEmit(value any) bool {
	exchange, ok := value.(HTTPExchange)
	if !ok {
		s.Warn(fmt.Sprintf("unexpected HTTP record %T", value))
		return false
	}
	target, err := url.Parse(exchange.Request.URL)
	if err != nil {
		s.Warn("HTTP capture received an invalid URL")
		return false
	}
	if !s.selection.Matches(exchange.Direction, target) {
		return true
	}
	if s.bodies == 0 {
		exchange.Request.PostData = nil
		exchange.Response.Content.Text = ""
	} else {
		if post := exchange.Request.PostData; post != nil && int64(len(post.Text)) > s.bodies {
			trimmed := *post
			trimmed.Text = trimmed.Text[:s.bodies]
			exchange.Request.PostData = &trimmed
		}
		if content := &exchange.Response.Content; int64(len(content.Text)) > s.bodies {
			content.Text = content.Text[:s.bodies]
			content.Truncated = true
		}
	}
	return s.Sink.TryEmit(exchange)
}

func recordHTTP(entry har.Entry, direction protocol.HTTPDirection) {
	httpSubscriptions.RLock()
	defer httpSubscriptions.RUnlock()
	for subscription := range httpSubscriptions.active {
		subscription.TryEmit(HTTPExchange{Entry: entry, Direction: direction})
	}
}

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
	var options struct {
		protocol.HTTPSelection
		Bodies string `json:"bodies"`
	}
	if err := json.Unmarshal(params, &options); err != nil {
		return nil, fmt.Errorf("HTTP capture options: %w", err)
	}
	if err := options.Validate(); err != nil {
		return nil, err
	}
	bodies, err := protocol.HTTPBodyLimit(options.Bodies)
	if err != nil {
		return nil, err
	}
	subscription := &httpSubscription{Sink: sink, selection: options.HTTPSelection, bodies: bodies}
	httpObserver.RLock()
	arm := httpObserver.arm
	httpObserver.RUnlock()
	release := func() error { return nil }
	if arm != nil && options.Direction != protocol.HTTPIncoming {
		release, err = arm(ctx, params, subscription)
		if err != nil {
			return nil, err
		}
	}
	httpSubscriptions.Lock()
	httpSubscriptions.active[subscription] = struct{}{}
	httpSubscriptions.Unlock()
	return func() error {
		httpSubscriptions.Lock()
		delete(httpSubscriptions.active, subscription)
		httpSubscriptions.Unlock()
		return release()
	}, nil
}

type httpTransport struct {
	next    http.RoundTripper
	capture http.RoundTripper
	wrap    func(http.RoundTripper) http.RoundTripper
}

func (t httpTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport := t.next
	if Armed("http") {
		transport = t.capture
	}
	if !Armed(httpTopKind) {
		return transport.RoundTrip(request)
	}
	finish := activeHTTPRequests.begin(request, protocol.HTTPOutgoing)
	response, err := transport.RoundTrip(request)
	if err != nil || response == nil || response.Body == nil || response.Body == http.NoBody || response.StatusCode == http.StatusSwitchingProtocols {
		finish()
	} else {
		response.Body = activeHTTPBody{ReadCloser: response.Body, finish: finish}
	}
	return response, err
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
		recordHTTP(*entry, protocol.HTTPOutgoing)
		return nil
	})
	wrap := collector.Middleware()
	return httpTransport{next: next, capture: wrap(next), wrap: wrap}
}

// InstrumentDefaultTransport wraps the default HTTP transport at application startup.
func InstrumentDefaultTransport() { http.DefaultTransport = Transport(http.DefaultTransport) }
