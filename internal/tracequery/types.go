package tracequery

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/flanksource/commons-db/connection/store"
)

var ErrTraceNotFound = errors.New("trace not found")

type DocumentFormat string

func (DocumentFormat) JSONSchema() map[string]any {
	return map[string]any{"type": "string", "enum": []string{"flat", "jaeger"}}
}

type Status string

func (Status) JSONSchema() map[string]any {
	return map[string]any{"type": "string", "enum": []string{"error", "ok"}}
}

type Span struct {
	TraceID    string          `json:"traceId"`
	SpanID     string          `json:"spanId"`
	ParentID   string          `json:"parentId,omitempty"`
	Service    string          `json:"service"`
	Operation  string          `json:"operation"`
	Kind       string          `json:"kind,omitempty"`
	StartedAt  time.Time       `json:"startedAt"`
	DurationMs float64         `json:"durationMs"`
	Status     string          `json:"status"`
	Attributes map[string]any  `json:"attributes,omitempty"`
	Resource   map[string]any  `json:"resource,omitempty"`
	OTLP       json.RawMessage `json:"otlp,omitempty"`
}

type Request struct {
	Source      string            `json:"source" clicky:"title=Source,required"`
	Run         string            `json:"run,omitempty" clicky:"title=Run,desc=Optional recorded run ID"`
	Index       string            `json:"index,omitempty" clicky:"title=Index,desc=OpenSearch index pattern (default otel-traces-*)"`
	Format      DocumentFormat    `json:"format,omitempty" clicky:"title=Document format"`
	Service     string            `json:"service,omitempty" clicky:"title=Service"`
	Operation   string            `json:"operation,omitempty" clicky:"title=Operation"`
	TraceID     string            `json:"traceId,omitempty" clicky:"title=Trace ID"`
	Lookback    string            `json:"lookback,omitempty" clicky:"title=Lookback,desc=Duration such as 15m or 24h; used when From is empty"`
	From        string            `json:"from,omitempty" clicky:"title=From,desc=Inclusive RFC3339 timestamp"`
	Until       string            `json:"until,omitempty" clicky:"title=Until,desc=Exclusive RFC3339 timestamp"`
	MinDuration string            `json:"minDuration,omitempty" clicky:"title=Minimum trace duration,desc=Duration such as 100ms or 2s"`
	MaxDuration string            `json:"maxDuration,omitempty" clicky:"title=Maximum trace duration,desc=Duration such as 100ms or 2s"`
	Status      Status            `json:"status,omitempty" clicky:"title=Status"`
	Tags        map[string]string `json:"tags,omitempty" clicky:"title=Tags,desc=Exact span or resource attribute values"`
	Limit       int               `json:"limit,omitempty" clicky:"title=Trace limit,desc=Maximum traces (1 to 1000); spans of a trace stay together"`
}

type Source struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}
type Summary struct {
	TraceID    string    `json:"traceId"`
	Operation  string    `json:"operation"`
	Service    string    `json:"service"`
	StartedAt  time.Time `json:"startedAt"`
	DurationMs float64   `json:"durationMs"`
	SpanCount  int       `json:"spanCount"`
	Errors     int       `json:"errors"`
	Services   []string  `json:"services"`
}
type SearchResult struct {
	Traces     []Summary `json:"traces"`
	Total      int       `json:"total"`
	Services   []string  `json:"services"`
	Operations []string  `json:"operations"`
}
type Detail struct {
	Summary        Summary  `json:"summary"`
	Spans          []Span   `json:"spans"`
	MissingParents []string `json:"missingParents"`
}

type Service struct {
	DataDir     string
	Connections *store.Store
}
