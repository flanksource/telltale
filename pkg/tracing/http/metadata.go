package http

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/flanksource/telltale/internal/protocol/httpdecode"
	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/tracing/seam"
	"github.com/flanksource/telltale/sdk/protocol"
)

type Direction = protocol.HTTPDirection

const (
	Incoming = protocol.HTTPIncoming
	Outgoing = protocol.HTTPOutgoing
	Both     = protocol.HTTPBoth
)

type Selection = protocol.HTTPSelection

type Metadata struct {
	Direction Direction           `json:"direction,omitempty" pretty:"label=Direction" filter:"terms"`
	RequestID string              `json:"requestId,omitempty"`
	Route     string              `json:"route,omitempty" filter:"text"`
	Handler   string              `json:"handler,omitempty" filter:"text"`
	Phase     string              `json:"phase,omitempty" filter:"terms"`
	ThreadID  int64               `json:"threadId,omitempty"`
	Thread    string              `json:"thread,omitempty"`
	TraceID   string              `json:"traceId,omitempty"`
	SpanID    string              `json:"spanId,omitempty"`
	Sampled   *bool               `json:"sampled,omitempty"`
	Protocol  string              `json:"protocol,omitempty" pretty:"label=Protocol" filter:"terms,options=git|github|kubernetes|anthropic|openai|s3|aws"`
	Operation string              `json:"operation,omitempty" pretty:"label=Operation" filter:"terms,lookup=false"`
	Decoded   *httpdecode.Details `json:"decoded,omitempty" pretty:"label=Decoded"`
}

// DecodeProtocol enriches a completed exchange before it enters the store.
func (e *Exchange) DecodeProtocol(options httpdecode.Options) {
	if e.Tunnelled {
		return
	}
	options.RequestEncoding, options.ResponseEncoding = e.RequestEncoding, e.Encoding
	result := httpdecode.Decode(e.Entry, options)
	e.Protocol, e.Operation, e.Decoded = result.Protocol, result.Operation, result.Details
	httpdecode.RedactSigning(&e.Entry)
	e.URL = e.Request.URL
}

func DecodeRecord(raw json.RawMessage) (Exchange, error) {
	var record Exchange
	if err := json.Unmarshal(raw, &record); err != nil {
		return record, fmt.Errorf("decode HTTP exchange: %w", err)
	}
	projected := FromEntry(record.Feature, record.Entry)
	var err error
	record.StartedAt, err = time.Parse(time.RFC3339Nano, record.StartedDateTime)
	if err != nil {
		return record, fmt.Errorf("HTTP exchange startedDateTime: %w", err)
	}
	record.Method, record.URL, record.Host = projected.Method, projected.URL, projected.Host
	record.Status, record.DurationMs = projected.Status, projected.DurationMs
	if record.Error == "" {
		record.Error = record.Entry.Error
	}
	record.Entry.Error = record.Error
	record.DecodeProtocol(httpdecode.Options{})
	return record, nil
}

func decodeBlob(blob agentclient.Blob, resource map[string]string) (seam.DecodedBlob[Exchange], error) {
	if blob.Info.ContentType != "application/json" {
		return seam.DecodedBlob[Exchange]{}, fmt.Errorf("HTTP exchanges require application/json, received %q", blob.Info.ContentType)
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(blob.Data, &raw); err != nil {
		return seam.DecodedBlob[Exchange]{}, fmt.Errorf("decode HTTP exchange batch: %w", err)
	}
	records := make([]Exchange, 0, len(raw))
	for _, item := range raw {
		record, err := DecodeRecord(item)
		if err != nil {
			return seam.DecodedBlob[Exchange]{}, err
		}
		record.Resource = resource
		records = append(records, record)
	}
	return seam.DecodedBlob[Exchange]{Rows: records}, nil
}
