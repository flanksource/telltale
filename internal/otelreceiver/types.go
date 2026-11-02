package otelreceiver

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	sdk "github.com/flanksource/telltale/sdk"
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

type batch struct {
	Spans    []sdk.Span
	Rejected int64
	Reason   string
}

// RestoreNative rebuilds a received span's attributes and resource from its
// native OTLP request, which keeps the 64-bit integers its stored JSON columns
// round. A span without a native request is returned as stored.
func RestoreNative(s sdk.Span) (sdk.Span, error) {
	if s.Native == "" {
		return s, nil
	}
	request, err := nativeRequest(s)
	if err != nil {
		return sdk.Span{}, err
	}
	resource := request.Traces().ResourceSpans().At(0)
	s.Resource = resource.Resource().Attributes().AsRaw()
	s.Attributes = resource.ScopeSpans().At(0).Spans().At(0).Attributes().AsRaw()
	return s, nil
}

// NativeJSON is the redacted OTLP request a received span was stored with.
func NativeJSON(s sdk.Span) (json.RawMessage, error) {
	request, err := nativeRequest(s)
	if err != nil {
		return nil, err
	}
	return request.MarshalJSON()
}

func nativeRequest(s sdk.Span) (ptraceotlp.ExportRequest, error) {
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
