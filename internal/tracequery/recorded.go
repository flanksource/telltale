package tracequery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/traces/read"
	imported "github.com/flanksource/telltale/pkg/tracing/opensearch"
	sdk "github.com/flanksource/telltale/sdk"
)

func (s Service) recorded(ctx context.Context, r Request) ([]Span, error) {
	runs, err := telltale.List(telltale.ListOptions{WorkDir: s.WorkDir})
	if err != nil {
		return nil, err
	}
	if r.Run != "" {
		run, err := telltale.Get(telltale.GetOptions{WorkDir: s.WorkDir, RunID: r.Run})
		if err != nil {
			return nil, err
		}
		runs = []telltale.RunInfo{run}
	}
	spans := []Span{}
	for _, run := range runs {
		kinds := map[string]bool{}
		for _, capture := range run.Captures {
			if capture.Kind == "spans" || capture.Kind == "opensearch" {
				kinds[capture.Kind] = true
			}
		}
		for kind := range kinds {
			rows, err := s.recordedRun(ctx, run.RunID, kind)
			if err != nil {
				return nil, err
			}
			spans = append(spans, rows...)
			if len(spans) > maxSpans {
				return nil, fmt.Errorf("recorded spans exceed %d; select a run", maxSpans)
			}
		}
	}
	return spans, nil
}

func (s Service) recordedRun(ctx context.Context, run, kind string) (_ []Span, err error) {
	selection, err := read.Resolve(kind, read.Scope{WorkDir: s.WorkDir, Run: run})
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, selection.Close()) }()
	spans := []Span{}
	for _, ref := range selection.Refs {
		_, err := ref.Runtime.Results.Backend.Meta(ctx, ref.Stream)
		// A newly armed capture has no stream until it emits its first record.
		if errors.Is(err, recordstore.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for row, err := range read.Rows(ctx, []read.Ref{ref}, read.Query{All: true}) {
			if err != nil {
				return nil, err
			}
			span, err := decodeRecorded(kind, row)
			if err != nil {
				return nil, fmt.Errorf("run %s %s span: %w", run, kind, err)
			}
			spans = append(spans, span)
			if len(spans) > maxSpans {
				return nil, fmt.Errorf("run %s exceeds %d spans", run, maxSpans)
			}
		}
	}
	return spans, nil
}

func decodeRecorded(kind string, row query.Row) (Span, error) {
	data, err := json.Marshal(row)
	if err != nil {
		return Span{}, err
	}
	if kind == "spans" {
		var span sdk.Span
		if err := json.Unmarshal(data, &span); err != nil {
			return Span{}, err
		}
		resource := make(map[string]any, len(span.Resource))
		for key, value := range span.Resource {
			resource[key] = value
		}
		return Span{TraceID: span.TraceID, SpanID: span.SpanID, ParentID: span.ParentID, Service: span.Resource["service.name"], Operation: span.Name, Kind: span.Kind, StartedAt: span.StartedAt, DurationMs: span.DurationMs, Status: span.Status, Attributes: span.Attrs, Resource: resource}, nil
	}
	var span imported.Span
	if err := json.Unmarshal(data, &span); err != nil {
		return Span{}, err
	}
	return fromImported(span), nil
}
