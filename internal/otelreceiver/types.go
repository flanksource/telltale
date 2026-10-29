package otelreceiver

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
)

const SourceID = "receiver"

var ErrLimit = errors.New("OTLP request limit exceeded")

type Options struct {
	Enabled         bool
	GRPCListen      string
	Retention       time.Duration
	MaxRequestBytes int
	MaxSpans        int
	MaxInflight     int
}

func DefaultOptions() Options {
	return Options{GRPCListen: "localhost:4317", Retention: 30 * 24 * time.Hour, MaxRequestBytes: 4 << 20, MaxSpans: 10000, MaxInflight: 8}
}

func (o Options) Validate() error {
	if o.Retention <= 0 || o.MaxRequestBytes <= 0 || o.MaxRequestBytes > 1<<30 || o.MaxSpans <= 0 || o.MaxInflight <= 0 || uint64(o.MaxInflight) > math.MaxUint32 {
		return fmt.Errorf("OTLP retention, request bytes (at most 1 GiB), span limit and inflight limit must be positive")
	}
	if o.GRPCListen == "" {
		return fmt.Errorf("OTLP gRPC listen address is required")
	}
	return nil
}

type Span struct {
	Key        string         `json:"key" filter:"exact"`
	TraceID    string         `json:"traceId" filter:"exact"`
	SpanID     string         `json:"spanId" filter:"exact"`
	ParentID   string         `json:"parentId,omitempty"`
	Service    string         `json:"service" filter:"terms"`
	Operation  string         `json:"operation" filter:"terms"`
	Kind       string         `json:"kind"`
	StartedAt  time.Time      `json:"startedAt"`
	DurationMs float64        `json:"durationMs"`
	Status     string         `json:"status" filter:"terms"`
	Attributes map[string]any `json:"-"`
	Resource   map[string]any `json:"-"`
	Native     string         `json:"native" filter:"-" pretty:"hide"`
	Digest     string         `json:"digest" filter:"-" pretty:"hide"`
	ReceivedAt time.Time      `json:"receivedAt" pretty:"hide"`
}

type batch struct {
	Spans    []Span
	Rejected int64
	Reason   string
}

func (s Span) NativeJSON() (json.RawMessage, error) {
	request, err := s.nativeRequest()
	if err != nil {
		return nil, err
	}
	return request.MarshalJSON()
}

func (s Span) nativeRequest() (ptraceotlp.ExportRequest, error) {
	encoded, err := base64.StdEncoding.DecodeString(s.Native)
	if err != nil {
		return ptraceotlp.ExportRequest{}, fmt.Errorf("decode stored OTLP span %s: %w", s.Key, err)
	}
	request := ptraceotlp.NewExportRequest()
	if err := request.UnmarshalProto(encoded); err != nil {
		return ptraceotlp.ExportRequest{}, fmt.Errorf("decode stored OTLP protobuf %s: %w", s.Key, err)
	}
	resources := request.Traces().ResourceSpans()
	if resources.Len() != 1 || resources.At(0).ScopeSpans().Len() != 1 || resources.At(0).ScopeSpans().At(0).Spans().Len() != 1 {
		return ptraceotlp.ExportRequest{}, fmt.Errorf("stored OTLP span %s must contain exactly one resource, scope and span", s.Key)
	}
	return request, nil
}
