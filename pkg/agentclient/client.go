package agentclient

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/flanksource/telltale/sdk/protocol"
)

// Agent is an authenticated connection to an agent, over the frame protocol
// (a local socket or the WebSocket hub) or direct HTTP to a deployed agent.
type Agent struct {
	Hello     protocol.Hello
	localPID  int
	transport transport
	mu        sync.Mutex
	done      chan struct{}
	doneOnce  sync.Once
	failure   error
}

// transport carries one agent's captures and calls. Session state that every
// transport shares (its channels, its end) stays on Session.
type transport interface {
	arm(ctx context.Context, s *Session, request ArmRequest) error
	checkpoint(ctx context.Context, s *Session, params json.RawMessage) (CheckpointResult, error)
	disarm(ctx context.Context, s *Session) error
	call(ctx context.Context, request CallRequest) (json.RawMessage, error)
	close() error
}

// ArmRequest selects a probe and its session settings.
type ArmRequest struct {
	Probe         string
	Params        json.RawMessage
	AllowMutation bool
	Mutates       bool
	StopAt        time.Time
	Limits        protocol.Limits
}

type CallRequest struct {
	Op            string
	Params        json.RawMessage
	AllowMutation bool
}

// Blob contains a blob's metadata and raw data.
type Blob struct {
	Info             protocol.Blob
	Data             []byte
	CheckpointParams json.RawMessage
}

type CheckpointResult struct {
	Records []protocol.Records
	Blobs   []Blob
}

type checkpointCall struct {
	result   CheckpointResult
	done     chan protocol.Checkpointed
	params   json.RawMessage
	doneSent bool
}

// Session receives events from an armed probe.
type Session struct {
	agent           *Agent
	ID              string
	Records         chan protocol.Records
	Blobs           chan Blob
	Reports         chan protocol.Report
	Warns           chan protocol.Warn
	Ended           chan protocol.Ended
	armed           chan protocol.Armed
	mu              sync.Mutex
	pending         map[string]*checkpointCall
	seenCheckpoints map[string]struct{}
	finished        chan struct{}
	end             protocol.Ended
	endOnce         sync.Once
	armedSeen       bool
	dropped         atomic.Uint64
}

// Dropped is the number of frames or records dropped by this client session.
func (s *Session) Dropped() uint64 { return s.dropped.Load() }

// Connect authenticates an already established stream.
func Connect(ctx context.Context, stream net.Conn, token string) (*Agent, error) {
	if deadline, ok := ctx.Deadline(); ok {
		if err := stream.SetDeadline(deadline); err != nil {
			return nil, err
		}
	}
	conn, hello, err := protocol.Authenticate(stream, token)
	if err != nil {
		_ = stream.Close()
		return nil, fmt.Errorf("connect agent: %w", err)
	}
	if err := stream.SetDeadline(time.Time{}); err != nil {
		_ = stream.Close()
		return nil, fmt.Errorf("clear agent deadline: %w", err)
	}
	return newWireAgent(&protocol.Client{Stream: stream, Conn: conn, Hello: hello}), nil
}

// Dial connects to a local discovery endpoint and verifies its identity.
func Dial(ctx context.Context, endpoint Endpoint) (*Agent, error) {
	network, err := protocol.LocalNetwork(endpoint.Addr)
	if err != nil {
		return nil, err
	}
	client, err := protocol.Dial(ctx, network, endpoint.Addr, endpoint.Token)
	if err != nil {
		return nil, fmt.Errorf("dial agent: %w", err)
	}
	if client.Hello.AgentID != strconv.Itoa(endpoint.PID) || !client.Hello.Started.Equal(endpoint.Started) {
		_ = client.Close()
		return nil, fmt.Errorf("%w: agent identity mismatch", protocol.ErrStaleEndpoint)
	}
	return newWireAgent(client), nil
}

// fail ends the connection once with cause; every later caller sees cause.
func (a *Agent) fail(cause error) {
	a.doneOnce.Do(func() {
		a.mu.Lock()
		a.failure = cause
		a.mu.Unlock()
		close(a.done)
	})
}

func newSession(agent *Agent) (*Session, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	return &Session{agent: agent, ID: hex.EncodeToString(id[:]), pending: make(map[string]*checkpointCall), seenCheckpoints: make(map[string]struct{}), Records: make(chan protocol.Records, 64), Blobs: make(chan Blob, 64), Reports: make(chan protocol.Report, 64), Warns: make(chan protocol.Warn, 64), Ended: make(chan protocol.Ended, 1), armed: make(chan protocol.Armed, 1), finished: make(chan struct{})}, nil
}

// Arm waits for the agent to accept a capture.
func (a *Agent) Arm(ctx context.Context, request ArmRequest) (*Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := a.validateArm(request); err != nil {
		return nil, err
	}
	session, err := newSession(a)
	if err != nil {
		return nil, err
	}
	if err := a.transport.arm(ctx, session, request); err != nil {
		return nil, err
	}
	return session, nil
}

func (a *Agent) validateArm(request ArmRequest) error {
	for _, probe := range a.Hello.Probes {
		if probe.Name != request.Probe {
			continue
		}
		if request.Mutates && !request.AllowMutation {
			return fmt.Errorf("probe %s: caller mutation disabled", request.Probe)
		}
		return nil
	}
	return fmt.Errorf("agent %s does not advertise probe %q", a.Hello.AgentID, request.Probe)
}

// Checkpoint requests an on-demand reading.
func (s *Session) Checkpoint(ctx context.Context, params json.RawMessage) (CheckpointResult, error) {
	return s.agent.transport.checkpoint(ctx, s, params)
}

// Disarm releases the capture.
func (s *Session) Disarm(ctx context.Context) error {
	select {
	case <-s.finished:
		return s.releaseError()
	default:
	}
	return s.agent.transport.disarm(ctx, s)
}

// awaitRelease waits for a requested disarm to end the session.
func (s *Session) awaitRelease(ctx context.Context) error {
	select {
	case <-s.finished:
		return s.releaseError()
	case <-s.agent.done:
		return fmt.Errorf("disarm: %w", s.agent.connectionError())
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Session) releaseError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.end.Error != "" {
		return fmt.Errorf("agent disarm: %s", s.end.Error)
	}
	return nil
}

// Call invokes an agent operation and waits for its result.
func (a *Agent) Call(ctx context.Context, request CallRequest) (json.RawMessage, error) {
	for _, operation := range a.Hello.Ops {
		if operation.Name == request.Op && operation.Mutates && !request.AllowMutation {
			return nil, fmt.Errorf("operation %s: caller mutation disabled", request.Op)
		}
	}
	return a.transport.call(ctx, request)
}

func (s *Session) endError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.end.Error != "" {
		return fmt.Errorf("agent session ended: %s", s.end.Error)
	}
	return errors.New("agent session ended")
}

func (a *Agent) connectionError() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.failure != nil {
		return a.failure
	}
	return errors.New("agent connection closed")
}

func (s *Session) finish(value protocol.Ended) {
	s.endOnce.Do(func() {
		value.Dropped += s.dropped.Load()
		s.mu.Lock()
		s.end = value
		close(s.finished)
		s.mu.Unlock()
		s.Ended <- value
	})
}

// Close closes the connection and all its captures.
func (a *Agent) Close() error { return a.transport.close() }

func deliver[T any](channel chan<- T, value T) bool {
	select {
	case channel <- value:
		return true
	default:
		return false
	}
}
