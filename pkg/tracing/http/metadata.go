package http

import (
	"encoding/json"
	"fmt"
	"path"
	"time"

	"github.com/flanksource/commons/properties"
	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/tracing/seam"
)

type Direction string

const (
	Incoming Direction = "incoming"
	Outgoing Direction = "outgoing"
	Both     Direction = "both"
)

func (Direction) JSONSchema() map[string]any {
	return map[string]any{"type": "string", "enum": []string{string(Incoming), string(Outgoing), string(Both)}, "default": string(Both)}
}

type Selection struct {
	Direction Direction `json:"direction,omitempty" clicky:"title=Direction,desc=Incoming requests outgoing clients or both"`
	Hosts     []string  `json:"hosts,omitempty" clicky:"title=Hosts,desc=Host globs to record"`
	Paths     []string  `json:"paths,omitempty" clicky:"title=Paths,desc=URL path globs to record"`
}

func (s Selection) Validate() error {
	if s.Direction != "" && s.Direction != Incoming && s.Direction != Outgoing && s.Direction != Both {
		return fmt.Errorf("direction %q must be incoming, outgoing or both", s.Direction)
	}
	for _, patterns := range []struct {
		name   string
		values []string
	}{{"hosts", s.Hosts}, {"paths", s.Paths}} {
		for _, pattern := range patterns.values {
			if _, err := path.Match(pattern, ""); err != nil || pattern == "" {
				return fmt.Errorf("%s contains an invalid glob %q", patterns.name, pattern)
			}
		}
	}
	return nil
}

type Metadata struct {
	Direction Direction `json:"direction,omitempty" filter:"terms"`
	RequestID string    `json:"requestId,omitempty"`
	Route     string    `json:"route,omitempty" filter:"text"`
	Handler   string    `json:"handler,omitempty" filter:"text"`
	Phase     string    `json:"phase,omitempty" filter:"terms"`
	ThreadID  int64     `json:"threadId,omitempty"`
	Thread    string    `json:"thread,omitempty"`
	TraceID   string    `json:"traceId,omitempty"`
	SpanID    string    `json:"spanId,omitempty"`
	Sampled   *bool     `json:"sampled,omitempty"`
}

func bodyLimit(value string) (int64, error) {
	if value == "" {
		return 256 << 10, nil
	}
	limit, err := properties.ParseBytes(value)
	if err != nil || limit < 0 || limit > 4<<20 {
		return 0, fmt.Errorf("bodies %q must be between zero and 4MiB", value)
	}
	return limit, nil
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
