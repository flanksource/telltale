// Package jms records JMS operations observed inside a connected JVM.
package jms

import (
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

const Kind = "jms"

type Options struct {
	Direction    string   `json:"direction,omitempty" clicky:"title=Direction,enum=both,incoming,outgoing"`
	Destinations []string `json:"destinations,omitempty" clicky:"title=Destinations,desc=Exact queue or topic names to observe; empty observes all"`
	Body         bool     `json:"body,omitempty" clicky:"title=Message bodies,desc=Capture bounded text bodies without consuming stream cursors or deserializing objects"`
	MaxBodyBytes int      `json:"maxBodyBytes,omitempty" clicky:"title=Body byte limit"`
	Capacity     int      `json:"capacity,omitempty" clicky:"title=Buffer capacity"`
}

func (o Options) Validate() error {
	if o.Direction != "" && o.Direction != "both" && o.Direction != "incoming" && o.Direction != "outgoing" {
		return fmt.Errorf("direction must be both, incoming or outgoing")
	}
	for _, destination := range o.Destinations {
		if strings.TrimSpace(destination) == "" {
			return fmt.Errorf("destinations must not contain blank names")
		}
	}
	if o.MaxBodyBytes < 0 || o.MaxBodyBytes > 1<<20 || (!o.Body && o.MaxBodyBytes != 0) {
		return fmt.Errorf("maxBodyBytes requires body capture and must be between zero and 1048576")
	}
	if o.Capacity < 0 || o.Capacity > 50000 {
		return fmt.Errorf("capacity must be between zero and 50000")
	}
	return nil
}

type Params struct {
	Target target.Target `json:"target,omitzero"`
	Options
}

func (p Params) Validate() error { return p.Options.Validate() }

type Row struct {
	tracing.Truncation
	StartedAt       time.Time         `json:"startedAt"`
	DurationMs      float64           `json:"durationMs"`
	Operation       string            `json:"operation" filter:"terms"`
	Direction       string            `json:"direction" filter:"terms"`
	Outcome         string            `json:"outcome" filter:"terms"`
	Destination     string            `json:"destination,omitempty" filter:"terms"`
	DestinationType string            `json:"destinationType,omitempty"`
	MessageID       string            `json:"messageId,omitempty"`
	CorrelationID   string            `json:"correlationId,omitempty"`
	Redelivered     *bool             `json:"redelivered,omitempty"`
	ReplyTo         string            `json:"replyTo,omitempty"`
	Subscription    string            `json:"subscription,omitempty"`
	MessageType     string            `json:"messageType,omitempty"`
	Framework       string            `json:"framework,omitempty"`
	Handler         string            `json:"handler,omitempty"`
	Transacted      *bool             `json:"transacted,omitempty"`
	AcknowledgeMode *int              `json:"acknowledgeMode,omitempty"`
	Body            string            `json:"body,omitempty"`
	BodyTruncated   bool              `json:"bodyTruncated,omitempty"`
	BodyUnsupported string            `json:"bodyUnsupported,omitempty"`
	Error           string            `json:"error,omitempty" filter:"text"`
	ErrorType       string            `json:"errorType,omitempty"`
	Phase           string            `json:"phase,omitempty" filter:"terms"`
	MetadataErrors  []string          `json:"metadataErrors,omitempty"`
	TraceID         string            `json:"traceId,omitempty"`
	SpanID          string            `json:"spanId,omitempty"`
	Resource        map[string]string `json:"resource,omitempty"`
}

func Decode(blob agentclient.Blob, resource map[string]string) ([]Row, error) {
	if blob.Info.ContentType != "application/json" {
		return nil, fmt.Errorf("JMS events require application/json, received %q", blob.Info.ContentType)
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(blob.Data, &raw); err != nil {
		return nil, fmt.Errorf("decode JMS events: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("JMS events must be an array, received null")
	}
	rows := make([]Row, 0, len(raw))
	for i, item := range raw {
		row, err := DecodeRecord(item)
		if err != nil {
			return nil, fmt.Errorf("JMS event %d: %w", i, err)
		}
		merged := maps.Clone(resource)
		if merged == nil {
			merged = map[string]string{}
		}
		maps.Copy(merged, row.Resource)
		row.Resource = merged
		rows = append(rows, row)
	}
	return rows, nil
}

func DecodeRecord(raw json.RawMessage) (Row, error) {
	var row Row
	if err := json.Unmarshal(raw, &row); err != nil {
		return row, fmt.Errorf("decode JMS event: %w", err)
	}
	if row.StartedAt.IsZero() || row.DurationMs < 0 {
		return row, fmt.Errorf("JMS event requires startedAt and nonnegative durationMs")
	}
	if row.Operation != "send" && row.Operation != "receive" && row.Operation != "process" {
		return row, fmt.Errorf("unknown JMS operation %q", row.Operation)
	}
	if (row.Operation == "send" && row.Direction != "outgoing") || (row.Operation != "send" && row.Direction != "incoming") {
		return row, fmt.Errorf("JMS %s has invalid direction %q", row.Operation, row.Direction)
	}
	if row.Outcome != "success" && row.Outcome != "error" && row.Outcome != "timeout" && row.Outcome != "incomplete" {
		return row, fmt.Errorf("unknown JMS outcome %q", row.Outcome)
	}
	if row.Outcome == "error" && row.Error == "" {
		return row, fmt.Errorf("failed JMS event requires error detail")
	}
	if row.Outcome == "timeout" && row.Operation != "receive" {
		return row, fmt.Errorf("only JMS receive operations can time out")
	}
	return row, nil
}
