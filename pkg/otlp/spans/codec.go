package spans

import (
	"encoding/base64"
	"errors"
	"fmt"

	sdk "github.com/flanksource/telltale/sdk"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
)

var ErrLimit = errors.New("OTLP request limit exceeded")

type Options struct {
	MaxSpans        int
	MaxRequestBytes int
}

type Batch struct {
	Spans    []sdk.Span
	Rejected int64
	Reason   string
}

func DecodeNative(encoded string) (sdk.Span, error) {
	if encoded == "" {
		return sdk.Span{}, errors.New("native OTLP span is required")
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return sdk.Span{}, fmt.Errorf("decode native OTLP span: %w", err)
	}
	request := ptraceotlp.NewExportRequest()
	if err := request.UnmarshalProto(data); err != nil {
		return sdk.Span{}, fmt.Errorf("decode native OTLP span: %w", err)
	}
	resources := request.Traces().ResourceSpans()
	if resources.Len() != 1 || resources.At(0).ScopeSpans().Len() != 1 || resources.At(0).ScopeSpans().At(0).Spans().Len() != 1 {
		return sdk.Span{}, errors.New("native OTLP span must contain exactly one resource, scope and span")
	}
	return convertSpan(spanContext{resource: resources.At(0), scope: resources.At(0).ScopeSpans().At(0), span: resources.At(0).ScopeSpans().At(0).Spans().At(0)})
}
