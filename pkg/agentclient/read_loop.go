package agentclient

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/flanksource/telltale/sdk/protocol"
)

var ErrProtocol = errors.New("agent protocol error")

func (a *Agent) readLoop() {
	var cause error
	defer func() {
		_ = a.stream.Close()
		a.mu.Lock()
		a.failure = cause
		sessions := make([]*Session, 0, len(a.sessions))
		for _, session := range a.sessions {
			sessions = append(sessions, session)
		}
		a.mu.Unlock()
		for _, session := range sessions {
			session.finish(protocol.Ended{Session: session.ID, Error: cause.Error()})
		}
		close(a.done)
	}()
	for {
		frame, data, err := a.conn.Read()
		if err != nil {
			cause = fmt.Errorf("agent connection closed: %w", err)
			return
		}
		if err := a.dispatchFrame(frame, data); err != nil {
			cause = err
			return
		}
	}
}

func (a *Agent) dispatchFrame(frame protocol.Envelope, data []byte) error {
	if frame.Type == protocol.TypeResult {
		return a.dispatchResult(frame.Body)
	}
	if frame.Type == protocol.TypePong {
		select {
		case a.pong <- struct{}{}:
		default:
		}
		return nil
	}
	var event struct {
		Session string `json:"session"`
	}
	if err := json.Unmarshal(frame.Body, &event); err != nil {
		return fmt.Errorf("%w: decode %s: %w", ErrProtocol, frame.Type, err)
	}
	if frame.Type == protocol.TypeArmed && event.Session == "" {
		return fmt.Errorf("%w: armed frame has no session", ErrProtocol)
	}
	a.mu.Lock()
	session := a.sessions[event.Session]
	a.mu.Unlock()
	if session == nil {
		return nil
	}
	return a.dispatchSession(session, frame, data)
}

func (a *Agent) dispatchResult(raw json.RawMessage) error {
	var result protocol.Result
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("%w: decode result: %w", ErrProtocol, err)
	}
	a.mu.Lock()
	call := a.calls[result.CallID]
	if a.callSeen[result.CallID] {
		a.mu.Unlock()
		return fmt.Errorf("%w: duplicate result %q", ErrProtocol, result.CallID)
	}
	a.callSeen[result.CallID] = true
	a.mu.Unlock()
	if call != nil && !deliver(call, result) {
		return fmt.Errorf("%w: duplicate result %q", ErrProtocol, result.CallID)
	}
	return nil
}

func (a *Agent) dispatchSession(s *Session, frame protocol.Envelope, data []byte) error {
	switch frame.Type {
	case protocol.TypeArmed:
		return s.receiveArmed(frame.Body)
	case protocol.TypeBlob:
		return s.receiveBlob(frame.Body, data)
	case protocol.TypeRecords:
		return s.receiveRecords(frame.Body)
	case protocol.TypeCheckpointed:
		return s.receiveCheckpointed(frame.Body)
	case protocol.TypeWarn, protocol.TypeError:
		var value protocol.Warn
		if err := json.Unmarshal(frame.Body, &value); err != nil {
			return fmt.Errorf("%w: decode %s: %w", ErrProtocol, frame.Type, err)
		}
		if !deliver(s.Warns, value) {
			s.dropped.Add(1)
		}
	case protocol.TypeReport:
		var value protocol.Report
		if err := json.Unmarshal(frame.Body, &value); err != nil {
			return fmt.Errorf("%w: decode report: %w", ErrProtocol, err)
		}
		if !deliver(s.Reports, value) {
			s.dropped.Add(1)
		}
	case protocol.TypeEnded:
		var value protocol.Ended
		if err := json.Unmarshal(frame.Body, &value); err != nil {
			return fmt.Errorf("%w: decode ended: %w", ErrProtocol, err)
		}
		s.finish(value)
		a.mu.Lock()
		delete(a.sessions, s.ID)
		a.mu.Unlock()
	default:
		return fmt.Errorf("%w: unexpected frame %q", ErrProtocol, frame.Type)
	}
	return nil
}

func (s *Session) receiveArmed(raw json.RawMessage) error {
	var value protocol.Armed
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("%w: decode armed: %w", ErrProtocol, err)
	}
	s.mu.Lock()
	duplicate := s.armedSeen
	s.armedSeen = true
	s.mu.Unlock()
	if duplicate || !deliver(s.armed, value) {
		return fmt.Errorf("%w: duplicate armed for %s", ErrProtocol, s.ID)
	}
	return nil
}

func (s *Session) receiveBlob(raw json.RawMessage, data []byte) error {
	var value protocol.Blob
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("%w: decode blob: %w", ErrProtocol, err)
	}
	if value.Checkpoint != "" {
		s.mu.Lock()
		if call := s.pending[value.Checkpoint]; call != nil {
			call.result.Blobs = append(call.result.Blobs, Blob{Info: value, Data: data, CheckpointParams: call.params})
		}
		s.mu.Unlock()
	} else if !deliver(s.Blobs, Blob{Info: value, Data: data}) {
		s.dropped.Add(1)
	}
	return nil
}

func (s *Session) receiveRecords(raw json.RawMessage) error {
	var value protocol.Records
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("%w: decode records: %w", ErrProtocol, err)
	}
	value.Dropped += s.dropped.Load()
	if value.Checkpoint != "" {
		s.mu.Lock()
		if call := s.pending[value.Checkpoint]; call != nil {
			call.result.Records = append(call.result.Records, value)
		}
		s.mu.Unlock()
	} else if !deliver(s.Records, value) {
		s.dropped.Add(uint64(len(value.Items)))
	}
	return nil
}

func (s *Session) receiveCheckpointed(raw json.RawMessage) error {
	var value protocol.Checkpointed
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("%w: decode checkpointed: %w", ErrProtocol, err)
	}
	if value.ID == "" {
		return fmt.Errorf("%w: checkpointed frame has no id", ErrProtocol)
	}
	s.mu.Lock()
	call := s.pending[value.ID]
	_, seen := s.seenCheckpoints[value.ID]
	if seen || (call != nil && call.doneSent) {
		s.mu.Unlock()
		return fmt.Errorf("%w: duplicate checkpointed %q", ErrProtocol, value.ID)
	}
	s.seenCheckpoints[value.ID] = struct{}{}
	if call != nil {
		call.doneSent = true
	}
	s.mu.Unlock()
	if call != nil && !deliver(call.done, value) {
		return fmt.Errorf("%w: duplicate checkpointed %q", ErrProtocol, value.ID)
	}
	return nil
}
