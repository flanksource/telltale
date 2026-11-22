package profiles

import "time"

const ContentType = "application/x-protobuf;message=opentelemetry.proto.profiles.v1development.ProfilesData"
const DevelopmentVersion = "1"
const Scope = "com.flanksource.telltale.top"
const ConventionVersion int64 = 2
const SampleKindAttribute = "telltale.sample.kind"
const SnapshotControlKind = "snapshot_control"

var sampleTypes = map[string]string{"http_top": "telltale.http.active.elapsed", "sql_top": "telltale.sql.active.elapsed", "jms_top": "telltale.jms.active.elapsed"}

func SampleType(kind string) string { return sampleTypes[kind] }

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
