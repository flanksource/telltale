package telltale

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/flanksource/telltale/sdk/protocol"
)

var ErrAgentMutationDisabled = errors.New("agent mutation disabled")
var ErrCallerMutationDisabled = errors.New("caller mutation disabled")

const maxBatchBytes = protocol.MaxFrameSize / 2

var active = struct {
	sync.RWMutex
	sessions map[string]map[*session]struct{}
}{sessions: make(map[string]map[*session]struct{})}
var armedCount atomic.Int64

// Armed reports whether any session is observing kind.
func Armed(kind string) bool {
	if armedCount.Load() == 0 {
		return false
	}
	active.RLock()
	defer active.RUnlock()
	return len(active.sessions[kind]) != 0
}

// Record sends a record to each session observing kind without blocking.
func Record(_ context.Context, kind string, record any) {
	if armedCount.Load() == 0 {
		return
	}
	active.RLock()
	defer active.RUnlock()
	for session := range active.sessions[kind] {
		session.TryEmit(record)
	}
}

type session struct {
	id          string
	probeName   string
	probe       Probe
	conn        *protocol.Conn
	cancel      context.CancelFunc
	disarm      func() error
	ring        chan json.RawMessage
	dropped     atomic.Uint64
	recordMu    sync.Mutex
	recordSeq   uint64
	blobSeq     atomic.Uint64
	once        sync.Once
	done        chan struct{}
	closed      chan struct{}
	stopRequest chan string
	limits      protocol.Limits
	limitMu     sync.Mutex
	records     int64
	bytes       int64
	limit       chan string
	rateAt      time.Time
	tokens      float64
	warnings    chan string
	onEnd       func()
}

type sessionMap struct {
	sync.Mutex
	values map[string]*session
}

func (m *sessionMap) get(id string) *session { m.Lock(); defer m.Unlock(); return m.values[id] }
func (m *sessionMap) add(s *session)         { m.Lock(); defer m.Unlock(); m.values[s.id] = s }
func (m *sessionMap) remove(s *session) {
	m.Lock()
	defer m.Unlock()
	if m.values[s.id] == s {
		delete(m.values, s.id)
	}
}
func (m *sessionMap) snapshot() []*session {
	m.Lock()
	defer m.Unlock()
	result := make([]*session, 0, len(m.values))
	for _, s := range m.values {
		result = append(result, s)
	}
	return result
}

func (a *Agent) arm(conn *protocol.Conn, sessions *sessionMap, raw json.RawMessage) {
	var request protocol.Arm
	if err := json.Unmarshal(raw, &request); err != nil {
		_ = send(conn, "armed", protocol.Armed{Error: err.Error()}, nil)
		return
	}
	probe, err := a.validateArm(request, sessions)
	if err != nil {
		_ = send(conn, "armed", protocol.Armed{Session: request.Session, Error: err.Error()}, nil)
		if errors.Is(err, ErrProbePanic) {
			_ = send(conn, "ended", protocol.Ended{Session: request.Session, Error: err.Error()}, nil)
		}
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &session{id: request.Session, probeName: request.Probe, probe: probe, conn: conn, cancel: cancel, ring: make(chan json.RawMessage, 256), done: make(chan struct{}), closed: make(chan struct{}), stopRequest: make(chan string, 1), limits: request.Limits, limit: make(chan string, 1), warnings: make(chan string, 16)}
	release, err := a.install(probe)
	if err != nil {
		cancel()
		_ = send(conn, "armed", protocol.Armed{Session: request.Session, Error: err.Error()}, nil)
		return
	}
	s.disarm, err = armProbe(probe, ctx, request.Params, s)
	if err == nil && s.disarm == nil {
		err = fmt.Errorf("probe %s arm returned no disarm", request.Probe)
	}
	if err != nil {
		cancel()
		err = errors.Join(err, release())
		_ = send(conn, "armed", protocol.Armed{Session: request.Session, Error: err.Error()}, nil)
		if errors.Is(err, ErrProbePanic) {
			_ = send(conn, "ended", protocol.Ended{Session: request.Session, Error: err.Error()}, nil)
		}
		return
	}
	disarm := s.disarm
	s.disarm = func() error { return errors.Join(disarm(), release()) }
	s.onEnd = func() { sessions.remove(s) }
	sessions.add(s)
	active.Lock()
	if active.sessions[request.Probe] == nil {
		active.sessions[request.Probe] = make(map[*session]struct{})
	}
	active.sessions[request.Probe][s] = struct{}{}
	armedCount.Add(1)
	active.Unlock()
	if send(conn, "armed", protocol.Armed{Session: s.id}, nil) != nil {
		go s.sendUntilEnded(request.StopAt)
		s.stop("")
		return
	}
	go s.sendUntilEnded(request.StopAt)
}

func (a *Agent) validateArm(request protocol.Arm, sessions *sessionMap) (Probe, error) {
	var err error
	catalog.RLock()
	probe := catalog.probes[request.Probe]
	catalog.RUnlock()
	if probe == nil {
		err = fmt.Errorf("unknown probe %q", request.Probe)
	} else if !a.probeAllowed(request.Probe) {
		err = fmt.Errorf("probe %q not allowed", request.Probe)
	}
	if request.Session == "" || sessions.get(request.Session) != nil {
		err = fmt.Errorf("invalid or duplicate session %q", request.Session)
	}
	if request.Limits.MaxRecords < 0 || request.Limits.MaxBytes < 0 || request.Limits.Rate < 0 || math.IsNaN(request.Limits.Rate) || math.IsInf(request.Limits.Rate, 0) {
		err = fmt.Errorf("invalid session limits")
	}
	var spec protocol.ProbeSpec
	if err == nil {
		spec, err = probeSpec(probe)
	}
	if err == nil && spec.Mutates {
		mutating := true
		if hook, ok := probe.(MutationProbe); ok {
			mutating, err = probeMutating(hook, request.Params)
		}
		if err == nil && mutating && !a.options.AllowMutation {
			err = fmt.Errorf("probe %s: %w", request.Probe, ErrAgentMutationDisabled)
		}
		if err == nil && mutating && !request.AllowMutation {
			err = fmt.Errorf("probe %s: %w", request.Probe, ErrCallerMutationDisabled)
		}
	}
	return probe, err
}

func (s *session) TryEmit(record any) bool {
	data, err := json.Marshal(record)
	if err != nil {
		s.dropped.Add(1)
		s.Warn(err.Error())
		return false
	}
	if len(data) > maxBatchBytes-1024 {
		s.dropped.Add(1)
		s.Warn("record exceeds maximum batch size")
		return false
	}
	s.limitMu.Lock()
	defer s.limitMu.Unlock()
	select {
	case <-s.closed:
		return false
	default:
	}
	if s.limits.Rate > 0 {
		now := time.Now()
		capacity := math.Max(1, s.limits.Rate)
		if s.rateAt.IsZero() {
			s.tokens = capacity
		} else {
			s.tokens = math.Min(capacity, s.tokens+now.Sub(s.rateAt).Seconds()*s.limits.Rate)
		}
		s.rateAt = now
		if s.tokens < 1 {
			s.dropped.Add(1)
			return false
		}
		s.tokens--
	}
	if (s.limits.MaxRecords > 0 && s.records >= s.limits.MaxRecords) || (s.limits.MaxBytes > 0 && s.bytes+int64(len(data)) > s.limits.MaxBytes) {
		select {
		case s.limit <- "session limit reached":
		default:
		}
		return false
	}
	select {
	case <-s.closed:
		return false
	case s.ring <- data:
		s.records++
		s.bytes += int64(len(data))
		if (s.limits.MaxRecords > 0 && s.records >= s.limits.MaxRecords) || (s.limits.MaxBytes > 0 && s.bytes >= s.limits.MaxBytes) {
			select {
			case s.limit <- "session limit reached":
			default:
			}
		}
		return true
	default:
		s.dropped.Add(1)
		return false
	}
}

func (s *session) Blob(name, contentType string, data []byte, meta any) error {
	metadata, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return send(s.conn, "blob", protocol.Blob{Session: s.id, Seq: s.blobSeq.Add(1), Name: name, ContentType: contentType, Len: len(data), Meta: metadata}, data)
}
func (s *session) Report(summary any) {
	data, err := json.Marshal(summary)
	if err == nil {
		_ = send(s.conn, "report", protocol.Report{Session: s.id, Summary: data}, nil)
	}
}
func (s *session) Warn(message string) {
	select {
	case s.warnings <- message:
	default:
	}
}

func (s *session) Fail(err error) { go s.stop(err.Error()) }

func (s *session) sendUntilEnded(stopAt time.Time) {
	ended := s.run(stopAt)
	s.onEnd()
	_ = send(s.conn, "ended", ended, nil)
	close(s.done)
}

type sessionBatch struct {
	session *session
	items   []json.RawMessage
	bytes   int
}

func (s *session) sendRecords(items []json.RawMessage, checkpoint string) error {
	s.recordMu.Lock()
	defer s.recordMu.Unlock()
	s.recordSeq++
	return send(s.conn, protocol.TypeRecords, protocol.Records{Session: s.id, Checkpoint: checkpoint, Seq: s.recordSeq, Items: items, Dropped: s.dropped.Load()}, nil)
}

func (b *sessionBatch) flush() error {
	if len(b.items) == 0 {
		return nil
	}
	err := b.session.sendRecords(b.items, "")
	if err != nil {
		b.session.dropped.Add(uint64(len(b.items)))
		_ = b.session.conn.Close()
	}
	b.items = nil
	b.bytes = 0
	return err
}

func (b *sessionBatch) append(item json.RawMessage) error {
	if b.bytes+len(item)+1 > maxBatchBytes-1024 {
		if err := b.flush(); err != nil {
			b.session.dropped.Add(1)
			return err
		}
	}
	b.items = append(b.items, item)
	b.bytes += len(item) + 1
	return nil
}

func (s *session) finish(batch *sessionBatch, message string) protocol.Ended {
	active.Lock()
	delete(active.sessions[s.probeName], s)
	armedCount.Add(-1)
	active.Unlock()
	s.limitMu.Lock()
	close(s.closed)
	s.limitMu.Unlock()
	s.cancel()
	var cause error
	if message != "" {
		cause = errors.New(message)
	}
	cause = errors.Join(cause, s.disarm())
	for len(s.ring) > 0 {
		if err := batch.append(<-s.ring); err != nil {
			cause = errors.Join(cause, err)
			s.dropped.Add(uint64(len(s.ring)))
			break
		}
	}
	cause = errors.Join(cause, batch.flush())
	if cause != nil {
		message = cause.Error()
	}
	return protocol.Ended{Session: s.id, Error: message, Dropped: s.dropped.Load()}
}

func (s *session) run(stopAt time.Time) protocol.Ended {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var timer <-chan time.Time
	if !stopAt.IsZero() {
		timeout := time.NewTimer(time.Until(stopAt))
		defer timeout.Stop()
		timer = timeout.C
	}
	batch := &sessionBatch{session: s, items: make([]json.RawMessage, 0, 128)}
	for {
		select {
		case item := <-s.ring:
			if err := batch.append(item); err != nil {
				return s.finish(batch, err.Error())
			}
			if len(batch.items) >= 128 {
				if err := batch.flush(); err != nil {
					return s.finish(batch, err.Error())
				}
			}
		case <-ticker.C:
			if err := batch.flush(); err != nil {
				return s.finish(batch, err.Error())
			}
		case message := <-s.warnings:
			_ = send(s.conn, "warn", protocol.Warn{Session: s.id, Message: message}, nil)
		case <-timer:
			return s.finish(batch, "")
		case message := <-s.limit:
			return s.finish(batch, message)
		case message := <-s.stopRequest:
			return s.finish(batch, message)
		}
	}
}

func (s *session) stop(message string) {
	s.once.Do(func() {
		select {
		case s.stopRequest <- message:
		case <-s.done:
		}
	})
	<-s.done
}
