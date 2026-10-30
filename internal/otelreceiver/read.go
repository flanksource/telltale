package otelreceiver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/telltale/pkg/traces/read"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/spans"
	sdk "github.com/flanksource/telltale/sdk"
)

func (s *Store) Read(ctx context.Context, options ReadOptions) ([]sdk.Span, error) {
	s.lifecycle.RLock()
	defer s.lifecycle.RUnlock()
	if s.closed {
		return nil, fmt.Errorf("OTLP store is closed")
	}
	stored, err := s.read(ctx, options)
	if err != nil {
		return nil, err
	}
	for i := range stored {
		if stored[i], err = RestoreNative(stored[i]); err != nil {
			return nil, err
		}
	}
	return stored, nil
}

func (s *Store) read(ctx context.Context, options ReadOptions) ([]sdk.Span, error) {
	_, err := s.results.Backend.Meta(ctx, stream)
	if errors.Is(err, recordstore.ErrNotFound) {
		return []sdk.Span{}, nil
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
	refs := []read.Ref{{Runtime: s.runtime, TraceRef: tracing.TraceRef{Kind: spans.Kind, Stream: stream}}}
	result := []sdk.Span{}
	for row, err := range read.Rows(ctx, refs, query) {
		if err != nil {
			return nil, err
		}
		span, err := spans.Decode(row)
		if err != nil {
			return nil, err
		}
		result = append(result, span)
		if len(result) > limit {
			return nil, fmt.Errorf("received OTLP spans exceed %d; narrow the time window", limit)
		}
	}
	return result, nil
}
