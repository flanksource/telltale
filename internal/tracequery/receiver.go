package tracequery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/flanksource/telltale/internal/otelreceiver"
)

func (s Service) received(ctx context.Context, r Request, ids []string) (_ []Span, err error) {
	if r.Run != "" || r.Index != "" || r.Format != "" {
		return nil, fmt.Errorf("received trace source does not accept run, index or format overrides")
	}
	store := s.Received
	if store == nil {
		store, err = otelreceiver.OpenStore(otelreceiver.StoreOptions{WorkDir: s.WorkDir, Retention: otelreceiver.DefaultOptions().Retention})
		if err != nil {
			return nil, err
		}
		defer func() { err = errors.Join(err, store.Close()) }()
	}
	options := otelreceiver.ReadOptions{TraceIDs: ids, Limit: maxSpans}
	if r.From != "" {
		options.From, err = time.Parse(time.RFC3339Nano, r.From)
		if err != nil {
			return nil, err
		}
	}
	if r.Until != "" {
		options.Until, err = time.Parse(time.RFC3339Nano, r.Until)
		if err != nil {
			return nil, err
		}
	}
	rows, err := store.Read(ctx, options)
	if err != nil {
		return nil, err
	}
	spans := make([]Span, 0, len(rows))
	for _, row := range rows {
		span := Span{TraceID: row.TraceID, SpanID: row.SpanID, ParentID: row.ParentID, Service: row.Service, Operation: row.Operation, Kind: row.Kind, StartedAt: row.StartedAt, DurationMs: row.DurationMs, Status: row.Status, Attributes: row.Attributes, Resource: row.Resource}
		if len(ids) > 0 {
			span.OTLP, err = row.NativeJSON()
			if err != nil {
				return nil, err
			}
		}
		spans = append(spans, span)
	}
	return spans, nil
}
