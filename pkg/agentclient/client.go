package agentclient

import (
	"bytes"
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

// Agent is an authenticated connection to a local agent.
type Agent struct {
	Hello    protocol.Hello
	localPID int
	stream   net.Conn
	conn     *protocol.Conn
	mu       sync.Mutex
	sessions map[string]*Session
	calls    map[string]chan protocol.Result
	callSeen map[string]bool
	done     chan struct{}
	pong     chan struct{}
	failure  error
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
	return newAgent(&protocol.Client{Stream: stream, Conn: conn, Hello: hello}), nil
}

func newAgent(client *protocol.Client) *Agent {
	agent := &Agent{Hello: client.Hello, stream: client.Stream, conn: client.Conn, sessions: make(map[string]*Session), calls: make(map[string]chan protocol.Result), callSeen: make(map[string]bool), done: make(chan struct{}), pong: make(chan struct{}, 1)}
	go agent.read()
	go agent.heartbeat()
	return agent
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
	return newAgent(client), nil
}

func (a *Agent) heartbeat() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := write(a.conn, "ping", protocol.Ping{}); err != nil {
				_ = a.stream.Close()
				return
			}
			timer := time.NewTimer(10 * time.Second)
			select {
			case <-a.pong:
				timer.Stop()
			case <-timer.C:
				_ = a.stream.Close()
				return
			case <-a.done:
				timer.Stop()
				return
			}
		case <-a.done:
			return
		}
	}
}

func write(conn *protocol.Conn, kind string, body any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	return conn.Write(protocol.Envelope{Type: kind, Body: data}, nil)
}

// Arm waits for the agent to accept a capture.
func (a *Agent) Arm(ctx context.Context, request ArmRequest) (*Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := a.validateArm(request); err != nil {
		return nil, err
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	s := &Session{agent: a, ID: hex.EncodeToString(id[:]), pending: make(map[string]*checkpointCall), seenCheckpoints: make(map[string]struct{}), Records: make(chan protocol.Records, 64), Blobs: make(chan Blob, 64), Reports: make(chan protocol.Report, 64), Warns: make(chan protocol.Warn, 64), Ended: make(chan protocol.Ended, 1), armed: make(chan protocol.Armed, 1), finished: make(chan struct{})}
	a.mu.Lock()
	a.sessions[s.ID] = s
	a.mu.Unlock()
	if err := write(a.conn, "arm", protocol.Arm{Session: s.ID, Probe: request.Probe, Params: request.Params, AllowMutation: request.AllowMutation, StopAt: request.StopAt, Limits: request.Limits}); err != nil {
		a.mu.Lock()
		delete(a.sessions, s.ID)
		a.mu.Unlock()
		return nil, err
	}
	select {
	case reply := <-s.armed:
		if reply.Error != "" {
			a.mu.Lock()
			delete(a.sessions, s.ID)
			a.mu.Unlock()
			return nil, errors.New(reply.Error)
		}
		return s, nil
	case <-ctx.Done():
		a.mu.Lock()
		delete(a.sessions, s.ID)
		a.mu.Unlock()
		go func() {
			if err := write(a.conn, protocol.TypeDisarm, protocol.Disarm{Session: s.ID}); err != nil {
				_ = a.stream.Close()
			}
		}()
		return nil, ctx.Err()
	case <-a.done:
		return nil, fmt.Errorf("arm: %w", a.connectionError())
	}
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
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return CheckpointResult{}, err
	}
	id := hex.EncodeToString(random[:])
	call := &checkpointCall{done: make(chan protocol.Checkpointed, 1), params: bytes.Clone(params)}
	s.mu.Lock()
	s.pending[id] = call
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.pending, id); s.mu.Unlock() }()
	if err := write(s.agent.conn, "checkpoint", protocol.Checkpoint{Session: s.ID, ID: id, Params: params}); err != nil {
		return CheckpointResult{}, fmt.Errorf("checkpoint: %w", err)
	}
	select {
	case done := <-call.done:
		if done.Error != "" {
			return call.result, fmt.Errorf("agent checkpoint: %s", done.Error)
		}
		return call.result, nil
	case <-s.finished:
		select {
		case done := <-call.done:
			if done.Error != "" {
				return call.result, fmt.Errorf("agent checkpoint: %s", done.Error)
			}
			return call.result, nil
		default:
		}
		return CheckpointResult{}, s.endError()
	case <-s.agent.done:
		return CheckpointResult{}, fmt.Errorf("checkpoint: %w", s.agent.connectionError())
	case <-ctx.Done():
		return CheckpointResult{}, ctx.Err()
	}
}

// Disarm releases the capture.
func (s *Session) Disarm(ctx context.Context) error {
	select {
	case <-s.finished:
		return s.releaseError()
	default:
	}
	if err := write(s.agent.conn, "disarm", protocol.Disarm{Session: s.ID}); err != nil {
		return fmt.Errorf("disarm: %w", err)
	}
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
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	callID := hex.EncodeToString(id[:])
	response := make(chan protocol.Result, 1)
	a.mu.Lock()
	a.calls[callID] = response
	a.mu.Unlock()
	defer func() { a.mu.Lock(); delete(a.calls, callID); a.mu.Unlock() }()
	if err := write(a.conn, "call", protocol.Call{CallID: callID, Op: request.Op, Params: request.Params, AllowMutation: request.AllowMutation}); err != nil {
		return nil, fmt.Errorf("agent call: %w", err)
	}
	select {
	case result := <-response:
		if result.Error != "" {
			return nil, fmt.Errorf("agent call %s: %s", request.Op, result.Error)
		}
		return result.Value, nil
	case <-a.done:
		return nil, fmt.Errorf("call: %w", a.connectionError())
	case <-ctx.Done():
		return nil, ctx.Err()
	}
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
func (a *Agent) Close() error { return a.stream.Close() }

func (a *Agent) read() { a.readLoop() }

func deliver[T any](channel chan<- T, value T) bool {
	select {
	case channel <- value:
		return true
	default:
		return false
	}
}
