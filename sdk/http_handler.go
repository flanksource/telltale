package telltale

import (
	"bytes"
	"context"
	"io"
	"math"
	"net/http"
	"time"

	"github.com/felixge/httpsnoop"
	"github.com/flanksource/commons/har"
	"github.com/flanksource/commons/http/middlewares"
)

type capturedBody struct {
	data  bytes.Buffer
	limit int64
	size  int64
}

func (b *capturedBody) Write(p []byte) (int, error) {
	b.size += int64(len(p))
	if remaining := b.limit + 1 - int64(b.data.Len()); remaining > 0 {
		_, _ = b.data.Write(p[:min(int64(len(p)), remaining)])
	}
	return len(p), nil
}

type tappedBody struct {
	io.Reader
	io.Closer
}

// Handler records requests handled by next without buffering the live response.
func Handler(next http.Handler) http.Handler {
	if next == nil {
		panic("telltale: HTTP handler must not be nil")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !Armed("http") {
			next.ServeHTTP(w, req)
			return
		}
		config := har.DefaultConfig()
		config.CaptureSensitive = false
		limit := config.MaxBodySize
		if limit <= 0 {
			limit = math.MaxInt64 - 1
		}
		requestBody, responseBody := &capturedBody{limit: limit}, &capturedBody{limit: limit}
		if req.Body != nil {
			req.Body = tappedBody{Reader: io.TeeReader(req.Body, requestBody), Closer: req.Body}
		}
		started := time.Now()
		status := http.StatusOK
		var header http.Header
		captureHeader := func(code int) {
			if code >= 200 && header == nil {
				status, header = code, w.Header().Clone()
			}
		}
		wrapped := httpsnoop.Wrap(w, httpsnoop.Hooks{
			WriteHeader: func(next httpsnoop.WriteHeaderFunc) httpsnoop.WriteHeaderFunc {
				return func(code int) { captureHeader(code); next(code) }
			},
			Write: func(next httpsnoop.WriteFunc) httpsnoop.WriteFunc {
				return func(p []byte) (int, error) {
					captureHeader(http.StatusOK)
					n, err := next(p)
					_, _ = responseBody.Write(p[:n])
					return n, err
				}
			},
			ReadFrom: func(next httpsnoop.ReadFromFunc) httpsnoop.ReadFromFunc {
				return func(r io.Reader) (int64, error) {
					captureHeader(http.StatusOK)
					return next(io.TeeReader(r, responseBody))
				}
			},
			WriteString: func(next httpsnoop.WriteStringFunc) httpsnoop.WriteStringFunc {
				return func(s string) (int, error) {
					captureHeader(http.StatusOK)
					n, err := next(s)
					_, _ = responseBody.Write([]byte(s[:n]))
					return n, err
				}
			},
		})
		next.ServeHTTP(wrapped, req)
		if header == nil {
			header = w.Header().Clone()
		}
		captureInbound(req, requestBody, started, config, func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Proto: req.Proto, Header: header, Body: io.NopCloser(bytes.NewReader(responseBody.data.Bytes())), ContentLength: responseBody.size}, nil
		})
	})
}

func captureInbound(req *http.Request, requestBody *capturedBody, started time.Time, config har.HARConfig, roundTrip middlewares.RoundTripperFunc) {
	copyReq := req.Clone(req.Context())
	urlCopy := *req.URL
	if urlCopy.Scheme == "" {
		urlCopy.Scheme = "http"
		if req.TLS != nil {
			urlCopy.Scheme = "https"
		}
	}
	if urlCopy.Host == "" {
		urlCopy.Host = req.Host
	}
	copyReq.URL = &urlCopy
	copyReq.Body = io.NopCloser(bytes.NewReader(requestBody.data.Bytes()))
	copyReq.ContentLength = requestBody.size
	config.MaxBodySize = requestBody.limit
	collector := har.NewCollectorWithLifecycle(config, nil, func(entry *har.Entry) error {
		entry.StartedDateTime = started.UTC().Format(time.RFC3339Nano)
		entry.Time = float64(time.Since(started)) / float64(time.Millisecond)
		Record(context.Background(), "http", entry)
		return nil
	})
	result, err := collector.Middleware()(roundTrip).RoundTrip(copyReq)
	if err != nil {
		active.RLock()
		for session := range active.sessions["http"] {
			session.Warn("inbound HTTP capture: " + err.Error())
		}
		active.RUnlock()
	}
	if result != nil && result.Body != nil {
		_ = result.Body.Close()
	}
}
