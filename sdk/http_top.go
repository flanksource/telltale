package telltale

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/flanksource/telltale/sdk/profiles"
	"github.com/flanksource/telltale/sdk/protocol"
	"github.com/google/uuid"
)

const httpTopKind = "http_top"
const httpTopSource = "telltale.source.instance"

type httpTopProbe struct{}

func init() { RegisterProbe(httpTopProbe{}) }

func (httpTopProbe) Spec() protocol.ProbeSpec {
	return protocol.ProbeSpec{Name: httpTopKind, Title: "Active HTTP requests", Checkpoints: true}
}

type httpTopOptions struct {
	protocol.HTTPSelection
	PollIntervalMS int64 `json:"pollIntervalMs"`
	MinElapsedNS   int64 `json:"minElapsedNs"`
	Limit          int   `json:"limit"`
}

type httpTopCapture struct {
	options httpTopOptions
	mu      sync.Mutex
	tick    int64
}

func (httpTopProbe) Arm(ctx context.Context, raw json.RawMessage, sink Sink) (func() error, error) {
	owner, ok := sink.(*session)
	if !ok {
		return nil, fmt.Errorf("HTTP Top requires an agent session")
	}
	options := httpTopOptions{PollIntervalMS: 5000, Limit: 50}
	if err := json.Unmarshal(raw, &options); err != nil {
		return nil, fmt.Errorf("HTTP Top options: %w", err)
	}
	if err := options.Validate(); err != nil {
		return nil, err
	}
	if options.PollIntervalMS <= 0 || options.PollIntervalMS > math.MaxInt64/int64(time.Millisecond) || options.MinElapsedNS < 0 || options.Limit < 1 || options.Limit > 10000 {
		return nil, fmt.Errorf("HTTP Top requires a positive sampling interval, nonnegative minimum elapsed time and limit between 1 and 10000")
	}
	owner.httpTop = &httpTopCapture{options: options}
	done := make(chan struct{})
	go func() {
		defer close(done)
		owner.httpTop.poll(ctx, owner)
	}()
	return func() error { <-done; return nil }, nil
}

func (c *httpTopCapture) poll(ctx context.Context, owner *session) {
	select {
	case <-ctx.Done():
		return
	case <-owner.ready:
	}
	ticker := time.NewTicker(time.Duration(c.options.PollIntervalMS) * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.publish(owner, "", owner); err != nil {
				owner.Fail(err)
				return
			}
		}
	}
}

func (httpTopProbe) Checkpoint(_ context.Context, _ json.RawMessage, sink Sink) error {
	checkpoint, ok := sink.(*checkpointSink)
	if !ok || checkpoint.session.httpTop == nil {
		return fmt.Errorf("HTTP Top requires an active checkpoint session")
	}
	return checkpoint.session.httpTop.publish(checkpoint.session, checkpoint.id, checkpoint)
}

func (c *httpTopCapture) publish(owner *session, checkpoint string, sink Sink) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	now, observed, rows := activeHTTPRequests.sample(c.options)
	c.tick++
	data, err := profiles.Encode([]profiles.Snapshot{{
		Kind: httpTopKind, CaptureID: owner.id, Source: owner.resource[httpTopSource], ProfileID: uuid.NewString(),
		Checkpoint: checkpoint, Sequence: c.tick, PollIntervalMS: c.options.PollIntervalMS,
		StartedAt: now, CompletedAt: now, Observed: observed, Limit: c.options.Limit, Complete: true, Rows: rows,
	}}, func(_ string, value any) any { return value })
	if err != nil {
		return fmt.Errorf("encode HTTP Top sample: %w", err)
	}
	return sink.Blob("http_top.otlp", profiles.ContentType, data, nil)
}

type activeHTTPRequest struct {
	id        uint64
	method    string
	started   time.Time
	direction protocol.HTTPDirection
	target    url.URL
}

type httpActivity struct {
	sync.Mutex
	sequence atomic.Uint64
	requests map[uint64]activeHTTPRequest
}

var activeHTTPRequests = httpActivity{requests: make(map[uint64]activeHTTPRequest)}

func (a *httpActivity) begin(request *http.Request, direction protocol.HTTPDirection) func() {
	target := httpTarget(request)
	target.User, target.RawQuery, target.Fragment, target.ForceQuery = nil, "", "", false
	id := a.sequence.Add(1)
	a.Lock()
	a.requests[id] = activeHTTPRequest{id: id, method: request.Method, started: time.Now(), direction: direction, target: target}
	a.Unlock()
	return func() {
		a.Lock()
		delete(a.requests, id)
		a.Unlock()
	}
}

func (a *httpActivity) sample(options httpTopOptions) (time.Time, int, []profiles.Row) {
	a.Lock()
	now := time.Now()
	var operations []activeHTTPRequest
	for _, operation := range a.requests {
		if now.Sub(operation.started).Nanoseconds() >= options.MinElapsedNS && options.Matches(operation.direction, &operation.target) {
			operations = append(operations, operation)
		}
	}
	a.Unlock()
	slices.SortFunc(operations, func(a, b activeHTTPRequest) int { return a.started.Compare(b.started) })
	observed := len(operations)
	operations = operations[:min(options.Limit, observed)]
	rows := make([]profiles.Row, 0, len(operations))
	for _, operation := range operations {
		rows = append(rows, profiles.Row{OperationID: strconv.FormatUint(operation.id, 10), ElapsedNS: now.Sub(operation.started).Nanoseconds(), SampledAt: now,
			Attributes: map[string]any{"direction": string(operation.direction), "method": operation.method, "host": operation.target.Host,
				"url": operation.target.String(), "startedAt": operation.started.UTC().Format(time.RFC3339Nano)}})
	}
	return now, observed, rows
}

type activeHTTPBody struct {
	io.ReadCloser
	finish func()
}

func (b activeHTTPBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		b.finish()
	}
	return n, err
}

func (b activeHTTPBody) Close() error {
	defer b.finish()
	return b.ReadCloser.Close()
}
