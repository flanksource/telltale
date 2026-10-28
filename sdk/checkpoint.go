package telltale

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/flanksource/telltale/sdk/protocol"
)

type checkpointSink struct {
	*session
	id      string
	mu      sync.Mutex
	err     error
	records []json.RawMessage
	bytes   int
}

func (s *checkpointSink) TryEmit(value any) bool {
	data, err := json.Marshal(value)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = errors.Join(s.err, err)
	if err != nil {
		return false
	}
	if len(s.records) >= 256 || s.bytes+len(data)+1 > maxBatchBytes-1024 {
		s.session.dropped.Add(1)
		return false
	}
	s.records = append(s.records, data)
	s.bytes += len(data) + 1
	return true
}

func (s *checkpointSink) Blob(name, contentType string, data []byte, meta any) error {
	metadata, err := json.Marshal(meta)
	if err == nil {
		err = send(s.conn, protocol.TypeBlob, protocol.Blob{Session: s.session.id, Checkpoint: s.id, Seq: s.session.blobSeq.Add(1), Name: name, ContentType: contentType, Len: len(data), Meta: metadata}, data)
	}
	s.mu.Lock()
	s.err = errors.Join(s.err, err)
	s.mu.Unlock()
	return err
}

func checkpoint(conn *protocol.Conn, sessions *sessionMap, request protocol.Checkpoint) bool {
	response := protocol.Checkpointed{Session: request.Session, ID: request.ID}
	var err error
	fatal := false
	session := sessions.get(request.Session)
	switch {
	case request.ID == "":
		err = errors.New("checkpoint id is required")
	case session == nil:
		err = ErrUnknownSession
	default:
		probe, ok := session.probe.(Checkpointer)
		if !ok {
			err = ErrCheckpointUnsupported
			break
		}
		sink := &checkpointSink{session: session, id: request.ID}
		probeErr := checkpointProbe(probe, request.Params, sink)
		sink.mu.Lock()
		err = errors.Join(probeErr, sink.err)
		if len(sink.records) > 0 {
			err = errors.Join(err, session.sendRecords(sink.records, request.ID))
		}
		sink.mu.Unlock()
		fatal = err != nil
	}
	if err != nil {
		response.Error = fmt.Sprintf("checkpoint: %v", err)
	}
	if send(conn, protocol.TypeCheckpointed, response, nil) != nil {
		return false
	}
	if fatal {
		session.stop(err.Error())
	}
	return true
}
