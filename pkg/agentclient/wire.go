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
	"sync"
	"time"

	"github.com/flanksource/telltale/sdk/protocol"
)

// wireTransport is the frame protocol over a local socket or the hub's
// WebSocket: one stream, multiplexed by session and call id.
type wireTransport struct {
	agent    *Agent
	stream   net.Conn
	conn     *protocol.Conn
	mu       sync.Mutex
	sessions map[string]*Session
	calls    map[string]chan protocol.Result
	callSeen map[string]bool
	pong     chan struct{}
}

func newWireAgent(client *protocol.Client) *Agent {
	agent := &Agent{Hello: client.Hello, done: make(chan struct{})}
	wire := &wireTransport{agent: agent, stream: client.Stream, conn: client.Conn, sessions: make(map[string]*Session), calls: make(map[string]chan protocol.Result), callSeen: make(map[string]bool), pong: make(chan struct{}, 1)}
	agent.transport = wire
	go wire.readLoop()
	go wire.heartbeat()
	return agent
}

func (w *wireTransport) heartbeat() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := write(w.conn, "ping", protocol.Ping{}); err != nil {
				_ = w.stream.Close()
				return
			}
			timer := time.NewTimer(10 * time.Second)
			select {
			case <-w.pong:
				timer.Stop()
			case <-timer.C:
				_ = w.stream.Close()
				return
			case <-w.agent.done:
				timer.Stop()
				return
			}
		case <-w.agent.done:
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

func (w *wireTransport) forget(s *Session) {
	w.mu.Lock()
	delete(w.sessions, s.ID)
	w.mu.Unlock()
}

func (w *wireTransport) arm(ctx context.Context, s *Session, request ArmRequest) error {
	w.mu.Lock()
	w.sessions[s.ID] = s
	w.mu.Unlock()
	if err := write(w.conn, "arm", protocol.Arm{Session: s.ID, Probe: request.Probe, Params: request.Params, AllowMutation: request.AllowMutation, StopAt: request.StopAt, Limits: request.Limits}); err != nil {
		w.forget(s)
		return err
	}
	select {
	case reply := <-s.armed:
		if reply.Error != "" {
			w.forget(s)
			return errors.New(reply.Error)
		}
		return nil
	case <-ctx.Done():
		w.forget(s)
		go func() {
			if err := write(w.conn, protocol.TypeDisarm, protocol.Disarm{Session: s.ID}); err != nil {
				_ = w.stream.Close()
			}
		}()
		return ctx.Err()
	case <-w.agent.done:
		return fmt.Errorf("arm: %w", w.agent.connectionError())
	}
}

func (w *wireTransport) checkpoint(ctx context.Context, s *Session, params json.RawMessage) (CheckpointResult, error) {
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
	if err := write(w.conn, "checkpoint", protocol.Checkpoint{Session: s.ID, ID: id, Params: params}); err != nil {
		return CheckpointResult{}, fmt.Errorf("checkpoint: %w", err)
	}
	select {
	case done := <-call.done:
		return checkpointed(call, done)
	case <-s.finished:
		select {
		case done := <-call.done:
			return checkpointed(call, done)
		default:
		}
		return CheckpointResult{}, s.endError()
	case <-w.agent.done:
		return CheckpointResult{}, fmt.Errorf("checkpoint: %w", w.agent.connectionError())
	case <-ctx.Done():
		return CheckpointResult{}, ctx.Err()
	}
}

func checkpointed(call *checkpointCall, done protocol.Checkpointed) (CheckpointResult, error) {
	if done.Error != "" {
		return call.result, fmt.Errorf("agent checkpoint: %s", done.Error)
	}
	return call.result, nil
}

func (w *wireTransport) disarm(ctx context.Context, s *Session) error {
	if err := write(w.conn, "disarm", protocol.Disarm{Session: s.ID}); err != nil {
		return fmt.Errorf("disarm: %w", err)
	}
	return s.awaitRelease(ctx)
}

func (w *wireTransport) call(ctx context.Context, request CallRequest) (json.RawMessage, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	callID := hex.EncodeToString(id[:])
	response := make(chan protocol.Result, 1)
	w.mu.Lock()
	w.calls[callID] = response
	w.mu.Unlock()
	defer func() { w.mu.Lock(); delete(w.calls, callID); w.mu.Unlock() }()
	if err := write(w.conn, "call", protocol.Call{CallID: callID, Op: request.Op, Params: request.Params, AllowMutation: request.AllowMutation}); err != nil {
		return nil, fmt.Errorf("agent call: %w", err)
	}
	select {
	case result := <-response:
		if result.Error != "" {
			return nil, fmt.Errorf("agent call %s: %s", request.Op, result.Error)
		}
		return result.Value, nil
	case <-w.agent.done:
		return nil, fmt.Errorf("call: %w", w.agent.connectionError())
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (w *wireTransport) close() error { return w.stream.Close() }
