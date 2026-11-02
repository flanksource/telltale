package otelreceiver

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/telltale/pkg/tracing/spans"
	sdk "github.com/flanksource/telltale/sdk"
)

// Persist appends the spans it has not stored yet: a retried span with the
// same digest is a duplicate, one with other content is rejected. Spans whose
// retention has passed are trimmed first, so their keys are free again.
func (s *Store) Persist(ctx context.Context, received []sdk.Span) (Outcome, error) {
	s.lifecycle.RLock()
	defer s.lifecycle.RUnlock()
	if s.closed || !s.options.Writable {
		return Outcome{}, fmt.Errorf("OTLP store is closed or read-only")
	}
	s.write.Lock()
	defer s.write.Unlock()
	if len(received) == 0 {
		return Outcome{}, nil
	}
	known, err := s.knownDigests(ctx, received)
	if err != nil {
		return Outcome{}, err
	}
	result := Outcome{}
	rows := make([]sdk.Span, 0, len(received))
	for _, span := range received {
		if digest, found := known[span.Key]; found {
			if digest == span.Digest {
				result.Duplicates++
			} else {
				result.Rejected++
				result.Reason = "conflicting content for an existing trace/span ID"
			}
			continue
		}
		known[span.Key] = span.Digest
		rows = append(rows, span)
	}
	if len(rows) == 0 {
		return result, nil
	}
	appended, err := recordstore.AppendTyped(ctx, s.results.Backend, stream, spans.Kind, rows)
	if err != nil {
		return Outcome{}, fmt.Errorf("persist OTLP spans: %w", err)
	}
	if appended.Skipped != 0 {
		return Outcome{}, fmt.Errorf("OTLP append unexpectedly skipped %d spans", appended.Skipped)
	}
	result.Accepted = int64(len(rows))
	return result, nil
}

// knownDigests are the digests of the stored spans among received, after
// trimming the spans appended longer than the retention ago.
func (s *Store) knownDigests(ctx context.Context, received []sdk.Span) (map[string]string, error) {
	_, err := s.results.Backend.Trim(ctx, stream, time.Now().Add(-s.options.Retention))
	if errors.Is(err, recordstore.ErrNotFound) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("trim expired OTLP spans: %w", err)
	}
	keys := make([]string, 0, len(received))
	for _, span := range received {
		keys = append(keys, span.Key)
	}
	existing, err := s.read(ctx, ReadOptions{Keys: keys, Limit: len(keys)})
	if err != nil {
		return nil, err
	}
	known := make(map[string]string, len(existing))
	for _, span := range existing {
		known[span.Key] = span.Digest
	}
	return known, nil
}
