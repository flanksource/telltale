// Package profilepublication atomically publishes profile rows, frames and durable identities.
package profilepublication

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/commons-db/recordstore/sampleframe"
	"github.com/google/uuid"
)

var ErrConflict = errors.New("conflicting content for a profile ID or source sequence")

// The record store elects one writer process; this lock serializes its profile identity checks and appends.
var publicationMu sync.Mutex

type Backend interface {
	sampleframe.Backend
	Scan(context.Context, string, int64, func(int64, recordstore.Row) error) error
}

type Publication struct {
	Kind              string
	Stream            string
	FrameStream       string
	Schema            recordstore.KindSchema
	FrameSchema       recordstore.KindSchema
	Frame             sampleframe.Frame
	Rows              []recordstore.Row
	AdditionalSchemas []recordstore.KindSchema
	AdditionalEntries []recordstore.BatchEntry
}

func Append(ctx context.Context, backend Backend, publication Publication) (bool, error) {
	if backend == nil || publication.Kind == "" || publication.Schema.Options.Key != "" {
		return false, fmt.Errorf("profile publication requires an atomic backend, kind and unkeyed row stream")
	}
	frame := publication.Frame
	if frame.Source == "" || frame.StartedAt.IsZero() || frame.CompletedAt.Before(frame.StartedAt) || frame.Observed < len(publication.Rows) || frame.Limit < 0 || (frame.Limit > 0 && len(publication.Rows) > frame.Limit) {
		return false, fmt.Errorf("profile publication requires ordered timestamps and observed count within its limit")
	}
	keys, digest, profileID, err := frameKeys(publication.Kind, frame)
	if err != nil {
		return false, err
	}
	publicationMu.Lock()
	defer publicationMu.Unlock()
	duplicate, err := duplicateProfile(ctx, backend, keys, digest)
	if err != nil || duplicate {
		return false, err
	}
	meta, err := backend.Meta(ctx, publication.Stream)
	if err != nil && !errors.Is(err, recordstore.ErrNotFound) {
		return false, err
	}
	frame.ID, frame.AfterSeq, frame.ToSeq, frame.Retained = profileID, meta.HighSeq, meta.HighSeq+int64(len(publication.Rows)), len(publication.Rows)
	publication.Frame = frame
	return appendProfile(ctx, backend, publication, keys, digest)
}

func duplicateProfile(ctx context.Context, backend Backend, keys []string, digest string) (bool, error) {
	found := map[string]bool{}
	err := backend.Scan(ctx, IdentityStream, 0, func(_ int64, row recordstore.Row) error {
		key, ok := row["key"].(string)
		if !ok {
			return fmt.Errorf("profile identity has no key")
		}
		previous, ok := row["digest"].(string)
		if !ok {
			return fmt.Errorf("profile identity has no digest")
		}
		if key != keys[0] && key != keys[1] {
			return nil
		}
		if previous != digest {
			return ErrConflict
		}
		found[key] = true
		return nil
	})
	if errors.Is(err, recordstore.ErrNotFound) {
		err = nil
	}
	if err == nil && len(found) == 1 {
		return false, ErrConflict
	}
	return len(found) == 2, err
}

func appendProfile(ctx context.Context, backend Backend, publication Publication, keys []string, digest string) (bool, error) {
	encoded, err := recordstore.EncodeRow(publication.Frame)
	if err != nil {
		return false, err
	}
	schema, err := ResultType().KindSchema()
	if err != nil {
		return false, err
	}
	identities := make([]recordstore.Row, len(keys))
	for index, key := range keys {
		identities[index], err = recordstore.EncodeRow(Identity{Key: key, Digest: digest, ProfileID: publication.Frame.ID, Kind: publication.Kind})
		if err != nil {
			return false, err
		}
	}
	entries := append([]recordstore.BatchEntry{
		{Op: recordstore.BatchAppend, Stream: publication.Stream, Kind: publication.Schema.Kind, Rows: publication.Rows},
		{Op: recordstore.BatchAppend, Stream: publication.FrameStream, Kind: publication.FrameSchema.Kind, Rows: []recordstore.Row{encoded}},
		{Op: recordstore.BatchAppend, Stream: IdentityStream, Kind: IdentityKind, Rows: identities},
	}, publication.AdditionalEntries...)
	schemas := append([]recordstore.KindSchema{publication.Schema, publication.FrameSchema, schema}, publication.AdditionalSchemas...)
	result, err := backend.AppendBatch(ctx, recordstore.Batch{ID: uuid.NewString(), Atomic: true, Producer: recordstore.Producer{Instance: publication.Frame.Source + "/" + publication.Kind, Seq: publication.Frame.Tick}, Schemas: schemas, Entries: entries})
	if err != nil {
		return false, err
	}
	if err := validateResult(result, len(entries), publication.Frame); err != nil {
		return false, err
	}
	return true, nil
}

func validateResult(result recordstore.BatchResult, expected int, frame sampleframe.Frame) error {
	if len(result.Entries) != expected {
		return fmt.Errorf("profile atomic batch expected %d entries, received %d", expected, len(result.Entries))
	}
	for _, entry := range result.Entries {
		if entry.Error != nil {
			return entry.Error
		}
	}
	if result.Entries[0].Append == nil || result.Entries[0].Append.Window != (recordstore.Window{From: frame.AfterSeq + 1, To: frame.ToSeq}) {
		return fmt.Errorf("profile row stream has another writer")
	}
	return nil
}
