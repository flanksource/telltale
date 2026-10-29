package otelreceiver

import (
	"context"
	"fmt"
	"time"

	"github.com/flanksource/commons-db/recordstore"
)

func (s *Store) Persist(ctx context.Context, spans []Span) (Outcome, error) {
	s.lifecycle.RLock()
	defer s.lifecycle.RUnlock()
	if s.closed || !s.options.Writable {
		return Outcome{}, fmt.Errorf("OTLP store is closed or read-only")
	}
	s.write.Lock()
	defer s.write.Unlock()
	keys := make([]string, 0, len(spans))
	for _, span := range spans {
		keys = append(keys, span.Key)
	}
	if len(keys) == 0 {
		return Outcome{}, nil
	}
	existing, err := s.read(ctx, ReadOptions{Keys: keys, Limit: len(keys)})
	if err != nil {
		return Outcome{}, err
	}
	known := map[string]string{}
	now := time.Now().UTC()
	for _, span := range existing {
		if !span.ReceivedAt.Before(now.Add(-s.options.Retention)) {
			known[span.Key] = span.Digest
		}
	}
	result := Outcome{}
	rows := make([]Span, 0, len(spans))
	for _, span := range spans {
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
		span.ReceivedAt = now
		rows = append(rows, span)
	}
	if len(rows) == 0 {
		return result, nil
	}
	appended, err := recordstore.AppendTyped(ctx, s.results.Backend, stream, kind, rows)
	if err != nil {
		return Outcome{}, fmt.Errorf("persist OTLP spans: %w", err)
	}
	if appended.Skipped != 0 {
		return Outcome{}, fmt.Errorf("OTLP append unexpectedly skipped %d spans", appended.Skipped)
	}
	result.Accepted = int64(len(rows))
	return result, nil
}
