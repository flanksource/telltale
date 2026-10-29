package otelreceiver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/telltale/pkg/traces/read"
	"github.com/flanksource/telltale/pkg/tracing"
)

func (s *Store) Read(ctx context.Context, options ReadOptions) ([]Span, error) {
	s.lifecycle.RLock()
	defer s.lifecycle.RUnlock()
	if s.closed {
		return nil, fmt.Errorf("OTLP store is closed")
	}
	return s.read(ctx, options)
}

func (s *Store) read(ctx context.Context, options ReadOptions) ([]Span, error) {
	_, err := s.results.Backend.Meta(ctx, stream)
	if errors.Is(err, recordstore.ErrNotFound) {
		return []Span{}, nil
	}
	if err != nil {
		return nil, err
	}
	query := read.Query{All: true, From: options.From, Until: options.Until}
	for column, values := range map[string][]string{"traceId": options.TraceIDs, "key": options.Keys} {
		if len(values) > 0 {
			query.Filters = append(query.Filters, column+"="+strings.Join(values, ","))
		}
	}
	limit := options.Limit
	if limit <= 0 {
		limit = 100000
	}
	refs := []read.Ref{{Runtime: s.runtime, TraceRef: tracing.TraceRef{Kind: kind, Stream: stream}}}
	spans := []Span{}
	for row, err := range read.Rows(ctx, refs, query) {
		if err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(row)
		if err != nil {
			return nil, err
		}
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		decoder.UseNumber()
		var span Span
		if err := decoder.Decode(&span); err != nil {
			return nil, err
		}
		native, err := span.nativeRequest()
		if err != nil {
			return nil, err
		}
		resource := native.Traces().ResourceSpans().At(0)
		span.Resource = resource.Resource().Attributes().AsRaw()
		span.Attributes = resource.ScopeSpans().At(0).Spans().At(0).Attributes().AsRaw()
		spans = append(spans, span)
		if len(spans) > limit {
			return nil, fmt.Errorf("received OTLP spans exceed %d; narrow the time window", limit)
		}
	}
	return spans, nil
}
