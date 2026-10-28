package protocol

import (
	"encoding/json"
	"fmt"
	"time"
)

const ProtocolVersion = 1

const (
	TypeAuth         = "auth"
	TypeHello        = "hello"
	TypeWelcome      = "welcome"
	TypeArm          = "arm"
	TypeArmed        = "armed"
	TypeCheckpoint   = "checkpoint"
	TypeCheckpointed = "checkpointed"
	TypeRecords      = "records"
	TypeBlob         = "blob"
	TypeReport       = "report"
	TypeWarn         = "warn"
	TypeDisarm       = "disarm"
	TypeEnded        = "ended"
	TypeCall         = "call"
	TypeResult       = "result"
	TypePing         = "ping"
	TypePong         = "pong"
	TypeError        = "error"
)

const (
	ContentTypePprof            = "application/vnd.google.protobuf+pprof"
	ContentTypeJFR              = "application/x-jfr"
	ContentTypeGoExecutionTrace = "application/x-go-exectrace"
	ContentTypeJavaVM           = "application/java-vm"
)

// CheckVersion refuses peers with a different protocol major version.
func CheckVersion(version int) error {
	if version != ProtocolVersion {
		return fmt.Errorf("protocol version mismatch: peer %d, local %d", version, ProtocolVersion)
	}
	return nil
}

type Envelope struct {
	Type string          `json:"t"`
	ID   string          `json:"id,omitempty"`
	Seq  uint64          `json:"seq,omitempty"`
	Body json.RawMessage `json:"body"`
}

type Runtime struct {
	Lang    string `json:"lang"`
	Version string `json:"version"`
	Vendor  string `json:"vendor"`
}
type ProbeSpec struct {
	Name         string          `json:"name"`
	Title        string          `json:"title"`
	ParamsSchema json.RawMessage `json:"paramsSchema,omitempty"`
	Checkpoints  bool            `json:"checkpoints"`
	Mutates      bool            `json:"mutates"`
	Reverts      bool            `json:"reverts"`
}
type OpSpec struct {
	Name         string          `json:"name"`
	ParamsSchema json.RawMessage `json:"paramsSchema,omitempty"`
	ResultSchema json.RawMessage `json:"resultSchema,omitempty"`
	Mutates      bool            `json:"mutates"`
}
type Limits struct {
	MaxRecords int64   `json:"maxRecords,omitempty"`
	MaxBytes   int64   `json:"maxBytes,omitempty"`
	Rate       float64 `json:"rate,omitempty"`
}
type Hello struct {
	Warnings   []string          `json:"warnings,omitempty"`
	AgentID    string            `json:"agentId"`
	Service    string            `json:"service"`
	Version    string            `json:"version"`
	SDKVersion string            `json:"sdkVersion"`
	Protocol   int               `json:"protocol"`
	Started    time.Time         `json:"started"`
	Runtime    Runtime           `json:"runtime"`
	Resource   map[string]string `json:"resource"`
	Labels     map[string]string `json:"labels"`
	Probes     []ProbeSpec       `json:"probes"`
	Ops        []OpSpec          `json:"ops"`
}
type Welcome struct {
	ServerID string `json:"serverId"`
	Accepted bool   `json:"accepted"`
	Error    string `json:"error,omitempty"`
	Token    string `json:"token,omitempty"`
}
type Arm struct {
	Session       string          `json:"session"`
	Probe         string          `json:"probe"`
	Params        json.RawMessage `json:"params"`
	StopAt        time.Time       `json:"stopAt,omitempty"`
	Limits        Limits          `json:"limits"`
	AllowMutation bool            `json:"allowMutation"`
}
type Armed struct {
	Session string `json:"session"`
	Error   string `json:"error,omitempty"`
}
type Checkpoint struct {
	Session string          `json:"session"`
	ID      string          `json:"id"`
	Params  json.RawMessage `json:"params"`
}
type Checkpointed struct {
	Session string `json:"session"`
	ID      string `json:"id"`
	Error   string `json:"error,omitempty"`
}
type Records struct {
	Session    string            `json:"session"`
	Checkpoint string            `json:"checkpoint,omitempty"`
	Seq        uint64            `json:"seq"`
	Items      []json.RawMessage `json:"items"`
	Dropped    uint64            `json:"dropped"`
}
type Blob struct {
	Session     string          `json:"session,omitempty"`
	Checkpoint  string          `json:"checkpoint,omitempty"`
	CallID      string          `json:"callId,omitempty"`
	Seq         uint64          `json:"seq"`
	Name        string          `json:"name"`
	ContentType string          `json:"contentType"`
	Len         int             `json:"len"`
	Meta        json.RawMessage `json:"meta,omitempty"`
}
type Report struct {
	Session string          `json:"session"`
	Summary json.RawMessage `json:"summary"`
}
type Warn struct {
	Session string `json:"session"`
	Message string `json:"message"`
}
type Disarm struct {
	Session string `json:"session"`
}
type Ended struct {
	Session string `json:"session"`
	Error   string `json:"error,omitempty"`
	Dropped uint64 `json:"dropped"`
}
type Call struct {
	CallID        string          `json:"callId"`
	Op            string          `json:"op"`
	Params        json.RawMessage `json:"params"`
	AllowMutation bool            `json:"allowMutation"`
}
type Result struct {
	CallID string          `json:"callId"`
	Value  json.RawMessage `json:"value"`
	Error  string          `json:"error,omitempty"`
}
type Ping struct{}
type Pong struct{}
