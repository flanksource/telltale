package sqltop

import (
	"context"
	"fmt"
	"sync"

	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/commons-db/recordstore/sampleframe"
	"github.com/flanksource/telltale/pkg/otlp/profilepublication"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/google/uuid"
)

type activeStore struct {
	mu        sync.Mutex
	backend   recordstore.BatchAppender
	stream    string
	sequence  int64
	activeSeq int64
	schemas   []recordstore.KindSchema
}

func newActiveStore(backend recordstore.BatchAppender, stream string) (*activeStore, error) {
	if backend == nil {
		return nil, fmt.Errorf("SQL top requires an atomic capture backend")
	}
	if err := recordstore.ValidateStream(stream); err != nil {
		return nil, err
	}
	nativeType := (handler{}).Schema()
	nativeType.Kind = "sql_top"
	nativeSchema, err := nativeType.KindSchema()
	if err != nil {
		return nil, err
	}
	activeSchema, err := ActiveResult().KindSchema()
	if err != nil {
		return nil, err
	}
	frameSchema, err := sampleframe.Schema("sql_top_frames")
	if err != nil {
		return nil, err
	}
	return &activeStore{backend: backend, stream: stream, schemas: []recordstore.KindSchema{nativeSchema, activeSchema, frameSchema}}, nil
}

func (s *activeStore) append(ctx context.Context, frame sampleframe.Frame, active []ActiveSample, native []Sample) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if frame.Source == "" || frame.StartedAt.IsZero() || frame.CompletedAt.Before(frame.StartedAt) || frame.Observed < len(active) || frame.Limit < 0 || (frame.Limit > 0 && len(active) > frame.Limit) {
		return fmt.Errorf("SQL top snapshot requires source, ordered timestamps and observed count within the limit")
	}
	activeRows, err := encodeRows(active)
	if err != nil {
		return err
	}
	nativeRows, err := encodeRows(native)
	if err != nil {
		return err
	}
	if profilepublication.IsProfile(frame) {
		return s.publishProfile(ctx, frame, activeRows, nativeRows)
	}
	frame.ID, frame.Result, frame.Tick = uuid.NewString(), "active", s.sequence+1
	frame.AfterSeq, frame.ToSeq, frame.Retained = s.activeSeq, s.activeSeq+int64(len(active)), len(active)
	encodedFrame, err := recordstore.EncodeRow(frame)
	if err != nil {
		return err
	}
	result, err := s.backend.AppendBatch(ctx, recordstore.Batch{ID: frame.ID, Atomic: true, Producer: recordstore.Producer{Instance: s.stream, Seq: s.sequence + 1}, Schemas: s.schemas, Entries: []recordstore.BatchEntry{
		{Op: recordstore.BatchAppend, Kind: "sql_top", Stream: s.stream, Rows: nativeRows},
		{Op: recordstore.BatchAppend, Kind: "sql_top_active", Stream: s.stream + "-active", Rows: activeRows},
		{Op: recordstore.BatchAppend, Kind: "sql_top_frames", Stream: s.stream + "-frames", Rows: []recordstore.Row{encodedFrame}},
	}})
	if err != nil {
		return err
	}
	if len(result.Entries) != 3 {
		return fmt.Errorf("SQL top atomic batch returned %d entries, expected 3", len(result.Entries))
	}
	for _, entry := range result.Entries {
		if entry.Error != nil {
			return entry.Error
		}
	}
	s.activeSeq, s.sequence = frame.ToSeq, s.sequence+1
	return nil
}

func (s *activeStore) publishProfile(ctx context.Context, frame sampleframe.Frame, active, native []recordstore.Row) error {
	backend, ok := s.backend.(profilepublication.Backend)
	if !ok {
		return fmt.Errorf("SQL profile capture requires durable identity reads and atomic publication")
	}
	frame.Result = "active"
	_, err := profilepublication.Append(ctx, backend, profilepublication.Publication{Kind: "sql_top", Stream: s.stream + "-active", FrameStream: s.stream + "-frames", Schema: s.schemas[1], FrameSchema: s.schemas[2], Frame: frame, Rows: active,
		AdditionalSchemas: []recordstore.KindSchema{s.schemas[0]}, AdditionalEntries: []recordstore.BatchEntry{{Op: recordstore.BatchAppend, Kind: "sql_top", Stream: s.stream, Rows: native}}})
	return err
}

func encodeRows[T any](values []T) ([]recordstore.Row, error) {
	rows := make([]recordstore.Row, len(values))
	for i, value := range values {
		row, err := recordstore.EncodeRow(value)
		if err != nil {
			return nil, err
		}
		rows[i] = row
	}
	return rows, nil
}

type activeEmitter struct {
	tracing.Emitter[Sample]
	store *activeStore
}

func (e activeEmitter) EmitFrame(ctx context.Context, frame sampleframe.Frame, rows []Sample) error {
	return e.store.append(ctx, frame, projectDatabase(frame.Source, rows), rows)
}
