// Package opensearch reads the span documents of an opentelemetry
// connection's index, over a window or followed: the spans kind's host seam.
package opensearch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/commons-db/query/esdsl"
	"github.com/flanksource/commons-db/query/providers"
	"github.com/flanksource/telltale/pkg/tracing"
	telltale "github.com/flanksource/telltale/sdk"
)

// Params choose the index and the window a capture reads.
type Params struct {
	// Connection is the opentelemetry connection whose OpenSearch index holds
	// the spans.
	Connection string `json:"connection" clicky:"required,title=Connection"`
	// Index, Format and DateField override the connection's defaults:
	// otel-traces-*, flat, and @timestamp.
	Index     string `json:"index,omitempty" clicky:"title=Index"`
	Format    string `json:"format,omitempty" clicky:"title=Format"`
	DateField string `json:"dateField,omitempty" clicky:"title=Date field"`
	// Search narrows the spans read.
	Search *esdsl.Search `json:"search,omitempty" clicky:"format=json,title=Structured search"`
	// From and To bound the window, as date math such as now-1h or RFC3339.
	From string `json:"from,omitempty" clicky:"title=From"`
	To   string `json:"to,omitempty" clicky:"title=To"`
	// FollowIndex goes on reading the spans indexed after the window, until the
	// session stops; it has no To.
	FollowIndex bool `json:"followIndex,omitempty" clicky:"title=Follow index"`
	// Poll and Lag, durations such as 2s, pace a follow and hold it behind now.
	Poll string `json:"poll,omitempty" clicky:"title=Poll"`
	Lag  string `json:"lag,omitempty" clicky:"title=Lag"`
}

func (p Params) Validate() error {
	if strings.TrimSpace(p.Connection) == "" {
		return errors.New("connection is required: the opentelemetry connection whose index holds the spans")
	}
	if p.FollowIndex && p.To != "" {
		return errors.New("a follow has no end: drop to, or read the window without follow")
	}
	if p.Format != "" && p.Format != "flat" && p.Format != "jaeger" {
		return fmt.Errorf("format %q is neither flat nor jaeger", p.Format)
	}
	_, _, err := p.pacing()
	return err
}

func (p Params) pacing() (poll, lag time.Duration, err error) {
	for _, option := range []struct {
		name, value string
		into        *time.Duration
	}{{"poll", p.Poll, &poll}, {"lag", p.Lag, &lag}} {
		if option.value == "" {
			continue
		}
		parsed, err := time.ParseDuration(option.value)
		if err != nil || parsed < 0 {
			return 0, 0, fmt.Errorf("%s %q is not a duration such as 2s", option.name, option.value)
		}
		*option.into = parsed
	}
	return poll, lag, nil
}

func (p Params) providerOptions() map[string]any {
	options := map[string]any{}
	for name, value := range map[string]string{"index": p.Index, "format": p.Format, "dateField": p.DateField} {
		if value != "" {
			options[name] = value
		}
	}
	if p.Search != nil {
		options["search"] = p.Search
	}
	return options
}

// FromRow is the span an opentelemetry row describes: resource.* attributes
// become its resource, the others its attributes, and its index and document
// id its source.
func FromRow(row query.Row) (telltale.Span, error) {
	text := func(key string) string {
		value, _ := row[key].(string)
		return value
	}
	span := telltale.Span{
		TraceID: text("trace_id"), SpanID: text("span_id"), ParentID: text("parent_id"),
		Service: text("service"), Operation: text("operation"), Status: text("status"),
		Attributes: map[string]any{}, Resource: map[string]any{},
		Source: telltale.SpanSource{Type: telltale.SpanSourceOpenSearch, Index: text("source_index"), ID: text("source_id")},
	}
	var err error
	if span.StartedAt, err = time.Parse(time.RFC3339Nano, text("timestamp")); err != nil {
		return span, fmt.Errorf("span document %s/%s timestamp: %w", span.Source.Index, span.Source.ID, err)
	}
	span.DurationMs, _ = row["duration_ms"].(float64)
	attributes, _ := row["_attributes"].(map[string]any)
	for key, value := range attributes {
		if name, ok := strings.CutPrefix(key, "resource."); ok {
			span.Resource[strings.TrimPrefix(name, "attributes.")] = value
		} else {
			span.Attributes[strings.TrimPrefix(key, "tag.")] = value
		}
	}
	if span.Service != "" {
		span.Resource["service.name"] = span.Service
	}
	if err := span.Normalize(); err != nil {
		return span, fmt.Errorf("span document %s/%s: %w", span.Source.Index, span.Source.ID, err)
	}
	return span, nil
}

// Handle reads the window, and with FollowIndex every span indexed after it,
// emitting each span it reads in order.
func Handle(ctx dbcontext.Context, params Params, records tracing.Emitter[telltale.Span]) error {
	poll, lag, err := params.pacing()
	if err != nil {
		return err
	}
	emitCtx := context.WithoutCancel(ctx)
	readCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var emitErr error
	readErr := providers.ReadOpenTelemetrySpans(ctx.Wrap(readCtx), providers.OpenTelemetryRead{
		Connection: params.Connection, Options: params.providerOptions(),
		From: params.From, To: params.To, Follow: params.FollowIndex, Poll: poll, Lag: lag,
	}, func(row query.Row) {
		if emitErr != nil {
			return
		}
		span, err := FromRow(row)
		if err == nil {
			err = records.Emit(emitCtx, span)
		}
		if err != nil {
			emitErr = err
			cancel()
		}
	})
	if emitErr != nil {
		return emitErr
	}
	return readErr
}
