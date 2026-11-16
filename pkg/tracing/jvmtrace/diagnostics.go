package jvmtrace

import (
	"encoding/json"
	"fmt"
	"time"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/recordstore/recordresults"
	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/seam"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

const NotificationsKind = "jvm_notifications"
const ExpressionKind = "jvm_ognl"

type NotificationOptions struct {
	MBean    string   `json:"mbean" clicky:"required,title=MBean or ObjectName pattern"`
	Types    []string `json:"types,omitempty" clicky:"title=Notification type prefixes"`
	Capacity int      `json:"capacity,omitempty" clicky:"title=Buffer capacity"`
}

func (o NotificationOptions) Validate() error {
	if o.MBean == "" {
		return fmt.Errorf("mbean is required")
	}
	if o.Capacity < 0 || o.Capacity > 50000 {
		return fmt.Errorf("capacity must be between one and 50000, or zero for the default")
	}
	return nil
}

type NotificationParams struct {
	Target target.Target `json:"target,omitzero"`
	NotificationOptions
}

func (p NotificationParams) Validate() error { return p.NotificationOptions.Validate() }

type Notification struct {
	Timestamp      time.Time         `json:"timestamp"`
	TimeStamp      int64             `json:"timeStamp"`
	Type           string            `json:"type"`
	SequenceNumber int64             `json:"sequenceNumber,omitempty"`
	Message        string            `json:"message,omitempty"`
	Source         string            `json:"source,omitempty"`
	UserData       json.RawMessage   `json:"userData,omitempty"`
	GC             json.RawMessage   `json:"gc,omitempty"`
	GCError        string            `json:"gcError,omitempty"`
	Dropped        int64             `json:"dropped,omitempty"`
	Resource       map[string]string `json:"resource,omitempty"`
}
type ExpressionOptions struct {
	Expression string `json:"expression" clicky:"required,title=OGNL expression,type=code"`
	Loader     string `json:"loader,omitempty" clicky:"title=Application loader hash"`
	IntervalMs int    `json:"intervalMs,omitempty" clicky:"title=Interval in milliseconds"`
}

func (o ExpressionOptions) Validate() error {
	if o.Expression == "" {
		return fmt.Errorf("expression is required")
	}
	if o.IntervalMs != 0 && (o.IntervalMs < 100 || o.IntervalMs > 3600000) {
		return fmt.Errorf("intervalMs must be between 100 and 3600000")
	}
	return nil
}

type ExpressionParams struct {
	Target target.Target `json:"target,omitzero"`
	ExpressionOptions
}

func (p ExpressionParams) Validate() error { return p.ExpressionOptions.Validate() }

type Expression struct {
	Timestamp  time.Time         `json:"timestamp"`
	Expression string            `json:"expression"`
	Result     json.RawMessage   `json:"result"`
	Resource   map[string]string `json:"resource,omitempty"`
}

type diagnostic[P, R any] struct {
	agent    seam.AgentSeam[P, R]
	defaults P
	schema   recordresults.ResultType[R]
}

func (h diagnostic[P, R]) Params() P                           { return h.defaults }
func (h diagnostic[P, R]) Schema() recordresults.ResultType[R] { return h.schema }
func (h diagnostic[P, R]) AgentAvailable(probe string, t target.Target) (bool, error) {
	return h.agent.Available(probe, t)
}
func (h diagnostic[P, R]) Prepare(ctx dbcontext.Context, p P, out tracing.Emitter[R]) (dbcontext.Context, func() error, error) {
	return h.agent.Prepare(ctx, p, out)
}
func (h diagnostic[P, R]) Handle(ctx dbcontext.Context, p P, out tracing.Emitter[R], _ tracing.Records[R]) error {
	return h.agent.Handle(ctx, p, out)
}
func (h diagnostic[P, R]) Checkpoint(ctx dbcontext.Context, _ P, out tracing.Emitter[R]) error {
	raw, err := checkpointParams(ctx)
	if err != nil {
		return err
	}
	return h.agent.Checkpoint(ctx, raw, out)
}

func NewNotificationKind(registry *agentclient.Registry) *tracing.Handler[NotificationParams, Notification] {
	agent := seam.AgentSeam[NotificationParams, Notification]{Probe: NotificationsKind, Registry: registry, Target: func(p NotificationParams) target.Target { return p.Target }, Mutates: func(NotificationParams) bool { return false }, ArmParams: func(p NotificationParams) (json.RawMessage, error) { return json.Marshal(p.NotificationOptions) }, DecodeBlob: func(b agentclient.Blob, resource map[string]string) (seam.DecodedBlob[Notification], error) {
		if b.Info.ContentType != "application/json" {
			return seam.DecodedBlob[Notification]{}, fmt.Errorf("notifications require application/json")
		}
		var rows []Notification
		if err := json.Unmarshal(b.Data, &rows); err != nil {
			return seam.DecodedBlob[Notification]{}, err
		}
		for i := range rows {
			rows[i].Timestamp = time.UnixMilli(rows[i].TimeStamp).UTC()
			rows[i].Resource = resource
		}
		return seam.DecodedBlob[Notification]{Rows: rows}, nil
	}}
	return tracing.NewHandler[NotificationParams, Notification](diagnostic[NotificationParams, Notification]{agent: agent, defaults: NotificationParams{NotificationOptions: NotificationOptions{MBean: "java.lang:type=GarbageCollector,*", Capacity: 500}}, schema: recordresults.ResultType[Notification]{Title: "JMX notifications and GC", TimeColumn: "timestamp", Follow: true, SearchColumns: []string{"type", "source", "message"}}}, tracing.Capabilities{Live: true}, target.Agent)
}
func NewExpressionKind(registry *agentclient.Registry) *tracing.Handler[ExpressionParams, Expression] {
	agent := seam.AgentSeam[ExpressionParams, Expression]{Probe: ExpressionKind, Registry: registry, Target: func(p ExpressionParams) target.Target { return p.Target }, Mutates: func(ExpressionParams) bool { return true }, ArmParams: func(p ExpressionParams) (json.RawMessage, error) { return json.Marshal(p.ExpressionOptions) }, DecodeBlob: func(b agentclient.Blob, resource map[string]string) (seam.DecodedBlob[Expression], error) {
		if b.Info.ContentType != "application/json" {
			return seam.DecodedBlob[Expression]{}, fmt.Errorf("expressions require application/json")
		}
		var rows []Expression
		if err := json.Unmarshal(b.Data, &rows); err != nil {
			return seam.DecodedBlob[Expression]{}, err
		}
		for i := range rows {
			rows[i].Resource = resource
		}
		return seam.DecodedBlob[Expression]{Rows: rows}, nil
	}}
	return tracing.NewHandler[ExpressionParams, Expression](diagnostic[ExpressionParams, Expression]{agent: agent, defaults: ExpressionParams{ExpressionOptions: ExpressionOptions{IntervalMs: 1000}}, schema: recordresults.ResultType[Expression]{Title: "OGNL samples", TimeColumn: "timestamp", Follow: true, SearchColumns: []string{"expression"}}}, tracing.Capabilities{Live: true}, target.Agent)
}
