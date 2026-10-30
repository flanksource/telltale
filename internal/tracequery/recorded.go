package tracequery

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/traces/read"
	"github.com/flanksource/telltale/pkg/tracing/spans"
)

func (s Service) recorded(ctx context.Context, r Request, ids []string) ([]Span, error) {
	query, err := recordedQuery(r, ids)
	if err != nil {
		return nil, err
	}
	runs, err := telltale.List(telltale.ListOptions{DataDir: s.DataDir})
	if err != nil {
		return nil, err
	}
	if r.Run != "" {
		run, err := telltale.Get(telltale.GetOptions{DataDir: s.DataDir, RunID: r.Run})
		if err != nil {
			return nil, err
		}
		runs = []telltale.RunInfo{run}
	}
	result := []Span{}
	for _, run := range runs {
		if !slices.ContainsFunc(run.Captures, func(capture telltale.CaptureResult) bool { return capture.Kind == spans.Kind }) {
			continue
		}
		rows, err := s.recordedRun(ctx, run.RunID, query, len(query.Filters) > 0)
		if err != nil {
			return nil, err
		}
		result = append(result, rows...)
		if len(result) > maxSpans {
			return nil, fmt.Errorf("recorded spans exceed %d; select a run", maxSpans)
		}
	}
	return result, nil
}

// recordedQuery pushes the search window, or the trace IDs a search found,
// down to the store, so a read loads only the spans it can return.
func recordedQuery(r Request, ids []string) (read.Query, error) {
	query := read.Query{All: true}
	if len(ids) == 0 && r.TraceID != "" {
		ids = []string{r.TraceID}
	}
	if len(ids) > 0 {
		query.Filters = []string{"traceId=" + strings.Join(ids, ",")}
	}
	for _, bound := range []struct {
		name, value string
		into        *time.Time
	}{{"from", r.From, &query.From}, {"until", r.Until, &query.Until}} {
		if bound.value == "" {
			continue
		}
		parsed, err := time.Parse(time.RFC3339Nano, bound.value)
		if err != nil {
			return read.Query{}, fmt.Errorf("%s: %w", bound.name, err)
		}
		*bound.into = parsed
	}
	return query, nil
}

// recordedRun reads run's spans; a read of whole traces (withOTLP) carries
// each received span's native OTLP request.
func (s Service) recordedRun(ctx context.Context, run string, query read.Query, withOTLP bool) (_ []Span, err error) {
	selection, err := read.Resolve(spans.Kind, read.Scope{DataDir: s.DataDir, Run: run})
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, selection.Close()) }()
	result := []Span{}
	for _, ref := range selection.Refs {
		_, err := ref.Runtime.Results.Backend.Meta(ctx, ref.Stream)
		// A newly armed capture has no stream until it emits its first record.
		if errors.Is(err, recordstore.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for row, err := range read.Rows(ctx, []read.Ref{ref}, query) {
			if err != nil {
				return nil, err
			}
			span, err := spans.Decode(row)
			if err != nil {
				return nil, fmt.Errorf("run %s span: %w", run, err)
			}
			converted, err := fromSpan(span, withOTLP)
			if err != nil {
				return nil, fmt.Errorf("run %s span %s: %w", run, span.Key, err)
			}
			result = append(result, converted)
			if len(result) > maxSpans {
				return nil, fmt.Errorf("run %s exceeds %d spans", run, maxSpans)
			}
		}
	}
	return result, nil
}
