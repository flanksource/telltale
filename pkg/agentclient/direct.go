package agentclient

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"sync"
	"time"

	"github.com/flanksource/telltale/sdk/protocol"
)

// HTTPEndpoint is a deployed agent a deployer recorded: its target id (the
// JWT audience and the agent's identity), its execution URL and the
// attributes a capture selects it by.
type HTTPEndpoint struct {
	ID       string
	URL      string
	Client   *http.Client
	Tokens   TokenSource
	Resource map[string]string
	// Poll is how often a streaming capture is read; zero reads at the
	// agent's own hub cadence, DefaultPoll.
	Poll time.Duration
}

// DefaultPoll matches the cadence the agent streams captures at over the hub.
const DefaultPoll = 250 * time.Millisecond

const (
	renewEvery     = 30 * time.Second
	requestTimeout = 30 * time.Second
	closeTimeout   = 10 * time.Second
)

// DialHTTP connects to a deployed agent over direct HTTP. It refuses an agent
// speaking another agent API rather than guessing at its routes.
func DialHTTP(ctx context.Context, endpoint HTTPEndpoint) (*Agent, error) {
	client := HTTPClient{Target: endpoint.ID, URL: endpoint.URL, Client: endpoint.Client, Tokens: endpoint.Tokens}
	hello, err := client.Hello(ctx)
	if err != nil {
		return nil, fmt.Errorf("dial agent %s: %w", endpoint.ID, err)
	}
	if hello.APIVersion != AgentAPIVersion {
		return nil, fmt.Errorf("dial agent %s: it speaks agent API %d, this client speaks %d", endpoint.ID, hello.APIVersion, AgentAPIVersion)
	}
	if err := protocol.CheckVersion(hello.Protocol); err != nil {
		return nil, fmt.Errorf("dial agent %s: %w", endpoint.ID, err)
	}
	poll := endpoint.Poll
	if poll == 0 {
		poll = DefaultPoll
	}
	resource := maps.Clone(endpoint.Resource)
	agent := &Agent{done: make(chan struct{}), Hello: protocol.Hello{AgentID: endpoint.ID, Service: resource["service.name"], Protocol: hello.Protocol, Started: hello.Started, Runtime: hello.Runtime, Resource: resource, Probes: hello.Probes, Ops: hello.Ops}}
	agent.transport = &httpTransport{agent: agent, client: client, poll: poll, ops: hello.Ops, captures: make(map[*Session]*httpCapture)}
	return agent, nil
}

type httpTransport struct {
	agent    *Agent
	client   HTTPClient
	poll     time.Duration
	mu       sync.Mutex
	ops      []protocol.OpSpec
	captures map[*Session]*httpCapture
}

// httpCapture is one armed capture: the agent holds its state, the client
// reads, renews and finishes it.
type httpCapture struct {
	Capture
	scope    string
	stop     chan struct{}
	stopOnce sync.Once
	seq      uint64
	sent     int64
	maxBytes int64
}

func (t *httpTransport) probeScope(name string) (string, error) {
	for _, probe := range t.agent.Hello.Probes {
		if probe.Name == name && probe.Scope != "" {
			return probe.Scope, nil
		}
	}
	return "", fmt.Errorf("agent %s names no scope for probe %q", t.agent.Hello.AgentID, name)
}

// opScope is the scope the agent's catalog names for op, re-reading the
// catalog once for an operation an extension added since the hello.
func (t *httpTransport) opScope(ctx context.Context, op string) (string, error) {
	if scope := t.lookupOp(op); scope != "" {
		return scope, nil
	}
	ops, err := t.client.Catalog(ctx)
	if err != nil {
		return "", fmt.Errorf("refresh catalog of %s: %w", t.agent.Hello.AgentID, err)
	}
	t.mu.Lock()
	t.ops = ops
	t.mu.Unlock()
	if scope := t.lookupOp(op); scope != "" {
		return scope, nil
	}
	return "", fmt.Errorf("agent %s does not advertise operation %q with a scope", t.agent.Hello.AgentID, op)
}

func (t *httpTransport) lookupOp(op string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, spec := range t.ops {
		if spec.Name == op {
			return spec.Scope
		}
	}
	return ""
}

func (t *httpTransport) call(ctx context.Context, request CallRequest) (json.RawMessage, error) {
	scope, err := t.opScope(ctx, request.Op)
	if err != nil {
		return nil, err
	}
	return t.client.Call(ctx, scope, request)
}

func (t *httpTransport) arm(ctx context.Context, s *Session, request ArmRequest) error {
	if !request.StopAt.IsZero() && !request.StopAt.After(time.Now()) {
		return errors.New("capture deadline already passed")
	}
	if request.Limits.MaxBytes < 0 {
		return errors.New("capture byte limit must not be negative")
	}
	scope, err := t.probeScope(request.Probe)
	if err != nil {
		return err
	}
	capture, err := t.client.Open(ctx, scope, request.Probe, request.Params, request.AllowMutation)
	if err != nil {
		return err
	}
	c := &httpCapture{Capture: capture, scope: scope, stop: make(chan struct{}), maxBytes: request.Limits.MaxBytes}
	t.mu.Lock()
	t.captures[s] = c
	t.mu.Unlock()
	go t.pump(s, c, request.StopAt)
	return nil
}

func (t *httpTransport) lookup(s *Session) *httpCapture {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.captures[s]
}

// pump reads a streaming capture, renews its lease, and ends it on
// completion, deadline, disarm or close.
func (t *httpTransport) pump(s *Session, c *httpCapture, stopAt time.Time) {
	var poll, deadline <-chan time.Time
	if c.Streaming {
		ticker := time.NewTicker(t.poll)
		defer ticker.Stop()
		poll = ticker.C
	}
	if !stopAt.IsZero() {
		timer := time.NewTimer(time.Until(stopAt))
		defer timer.Stop()
		deadline = timer.C
	}
	renew := time.NewTicker(renewEvery)
	defer renew.Stop()
	for {
		select {
		case <-poll:
			complete, err := t.stream(s, c)
			if err != nil || complete {
				t.end(s, c, err, err == nil)
				return
			}
		case <-renew.C:
			if err := t.request(func(ctx context.Context) error { return t.client.Renew(ctx, c.scope, c.ID) }); err != nil {
				t.end(s, c, err, false)
				return
			}
		case <-deadline:
			t.end(s, c, nil, true)
			return
		case <-c.stop:
			t.end(s, c, nil, false)
			return
		}
	}
}

func (t *httpTransport) request(run func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	return run(ctx)
}

func (t *httpTransport) stream(s *Session, c *httpCapture) (bool, error) {
	var dump Dump
	err := t.request(func(ctx context.Context) (err error) { dump, err = t.client.Dump(ctx, c.scope, c.ID); return err })
	if err != nil {
		return false, err
	}
	blob, err := t.blob(s, c, "", dump.Data)
	if err != nil {
		return false, err
	}
	if !deliver(s.Blobs, blob) {
		s.dropped.Add(1)
	}
	return dump.Complete, nil
}

func (t *httpTransport) blob(s *Session, c *httpCapture, checkpoint string, data []byte) (Blob, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if c.maxBytes > 0 && int64(len(data)) > c.maxBytes-c.sent {
		return Blob{}, errors.New("capture byte limit exceeded")
	}
	c.sent += int64(len(data))
	c.seq++
	return Blob{Info: protocol.Blob{Session: s.ID, Checkpoint: checkpoint, Seq: c.seq, Name: c.FileName, ContentType: c.ContentType, Len: len(data)}, Data: data}, nil
}

// end finishes the capture on the agent, which restores what it changed,
// delivers its last reading and ends the session.
func (t *httpTransport) end(s *Session, c *httpCapture, cause error, complete bool) {
	var final Dump
	err := t.request(func(ctx context.Context) (err error) { final, err = t.client.Finish(ctx, c.scope, c.ID); return err })
	if err == nil {
		var blob Blob
		if blob, err = t.blob(s, c, "", final.Data); err == nil && !deliver(s.Blobs, blob) {
			s.dropped.Add(1)
		}
	}
	t.mu.Lock()
	delete(t.captures, s)
	t.mu.Unlock()
	ended := protocol.Ended{Session: s.ID, Complete: complete && err == nil}
	if failure := errors.Join(cause, err); failure != nil {
		ended.Error = failure.Error()
	}
	s.finish(ended)
}

func (t *httpTransport) checkpoint(ctx context.Context, s *Session, params json.RawMessage) (CheckpointResult, error) {
	c := t.lookup(s)
	if c == nil {
		return CheckpointResult{}, s.endError()
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return CheckpointResult{}, err
	}
	dump, err := t.client.Dump(ctx, c.scope, c.ID)
	if err != nil {
		return CheckpointResult{}, fmt.Errorf("agent checkpoint: %w", err)
	}
	blob, err := t.blob(s, c, hex.EncodeToString(id[:]), dump.Data)
	if err != nil {
		return CheckpointResult{}, err
	}
	blob.CheckpointParams = params
	return CheckpointResult{Blobs: []Blob{blob}}, nil
}

func (t *httpTransport) disarm(ctx context.Context, s *Session) error {
	if c := t.lookup(s); c != nil {
		c.stopOnce.Do(func() { close(c.stop) })
	}
	return s.awaitRelease(ctx)
}

// close finishes every live capture before the agent counts as gone, so no
// instrumentation outlives the client.
func (t *httpTransport) close() error {
	t.mu.Lock()
	live := maps.Clone(t.captures)
	t.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
	defer cancel()
	var errs []error
	for session, c := range live {
		c.stopOnce.Do(func() { close(c.stop) })
		errs = append(errs, session.awaitRelease(ctx))
	}
	t.agent.fail(errors.New("agent connection closed"))
	return errors.Join(errs...)
}
