// Package profiles encodes Telltale active-operation snapshots as OTLP Profiles.
package profiles

import (
	"encoding/base64"
	"errors"
	"time"

	"github.com/flanksource/commons-db/recordstore/sampleframe"
)

const ContentType = "application/x-protobuf;message=opentelemetry.proto.profiles.v1development.ProfilesData"
const DevelopmentVersion = "1"
const Scope = "com.flanksource.telltale.top"
const conventionVersion int64 = 2
const sampleKindAttribute = "telltale.sample.kind"
const snapshotControlKind = "snapshot_control"

var sampleTypes = map[string]string{"http_top": "telltale.http.active.elapsed", "sql_top": "telltale.sql.active.elapsed", "jms_top": "telltale.jms.active.elapsed"}

var ErrLimit = errors.New("OTLP Profiles limit exceeded")

type Options struct {
	MaxBytes, MaxProfiles, MaxSamples               int
	ExpectedKind, ExpectedCaptureID, ExpectedSource string
}

type Row struct {
	OperationID string         `json:"operationId"`
	ElapsedNS   int64          `json:"elapsedNs"`
	SampledAt   time.Time      `json:"sampledAt"`
	Attributes  map[string]any `json:"attributes"`
	TraceID     string         `json:"traceId,omitempty"`
	SpanID      string         `json:"spanId,omitempty"`
	Stack       []string       `json:"stack,omitempty"`
}

type Snapshot struct {
	Kind, CaptureID, Source, ProfileID, Checkpoint string
	Sequence                                       int64
	PollIntervalMS                                 int64
	StartedAt, CompletedAt                         time.Time
	Observed, Limit                                int
	Dropped                                        uint64
	Complete                                       bool
	Rows                                           []Row
	Resource                                       map[string]any
	Native                                         []byte
	Digest                                         string
}

func (s Snapshot) Frame() sampleframe.Frame {
	frame := sampleframe.Frame{Source: s.Source + "/" + s.CaptureID, Tick: s.Sequence, StartedAt: s.StartedAt, CompletedAt: s.CompletedAt,
		Observed: s.Observed, Limit: s.Limit, Summary: map[string]any{"profileId": s.ProfileID, "captureId": s.CaptureID, "checkpoint": s.Checkpoint,
			"receivedAt": time.Now().UTC().Format(time.RFC3339Nano), "dropped": s.Dropped, "complete": s.Complete, "otlp": base64.StdEncoding.EncodeToString(s.Native), "digest": s.Digest, "profilesDevelopmentVersion": DevelopmentVersion}}
	if s.PollIntervalMS > 0 {
		frame.Summary["pollIntervalMs"] = s.PollIntervalMS
	}
	return frame
}
