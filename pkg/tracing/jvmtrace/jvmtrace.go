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

const Kind = "jvm_trace"

type MethodTarget struct {
	Class  string `json:"class" clicky:"required,title=Class"`
	Method string `json:"method" clicky:"required,title=Method"`
	Loader string `json:"loader,omitempty" clicky:"title=Class loader"`
}

type Options struct {
	Targets   []MethodTarget `json:"targets" clicky:"required,title=Methods"`
	Args      bool           `json:"args,omitempty" clicky:"title=Arguments"`
	Return    bool           `json:"return,omitempty" clicky:"title=Return values"`
	Exception bool           `json:"exception,omitempty" clicky:"title=Exceptions"`
	Calls     bool           `json:"calls,omitempty" clicky:"title=Nested calls"`
	Platform  bool           `json:"platform,omitempty" clicky:"title=Platform calls"`
	Events    int            `json:"events,omitempty" clicky:"title=Event limit"`
	Expand    int            `json:"expand,omitempty" clicky:"title=Object graph depth"`
	Chars     int            `json:"arg-chars,omitempty" clicky:"title=String character limit"`
	GC        bool           `json:"gc,omitempty" clicky:"title=GC counters"`
	Alloc     bool           `json:"alloc,omitempty" clicky:"title=Allocation counters"`
}

func (o Options) Validate() error {
	if len(o.Targets) == 0 || len(o.Targets) > 8 {
		return fmt.Errorf("targets must contain one to eight methods")
	}
	for _, t := range o.Targets {
		if t.Class == "" || t.Method == "" {
			return fmt.Errorf("each target requires class and method")
		}
	}
	if o.Events < 0 || o.Expand < 0 || o.Expand > 10 || o.Chars < 0 {
		return fmt.Errorf("event and character limits must be nonnegative; graph depth must be between zero and ten")
	}
	if o.Platform && !o.Calls {
		return fmt.Errorf("platform requires calls")
	}
	return nil
}

type Params struct {
	Target target.Target `json:"target,omitzero"`
	Options
}

func (p Params) Validate() error { return p.Options.Validate() }

type Event struct {
	Timestamp  time.Time         `json:"timestamp"`
	TS         int64             `json:"ts"`
	ProbeSeq   int64             `json:"probeSeq"`
	Kind       string            `json:"kind"`
	Key        string            `json:"key"`
	ThreadID   int64             `json:"threadId"`
	Thread     string            `json:"thread,omitempty"`
	Depth      int               `json:"depth"`
	Frame      int64             `json:"frame"`
	Parent     int64             `json:"parent,omitempty"`
	Class      string            `json:"class,omitempty"`
	Method     string            `json:"method,omitempty"`
	Descriptor string            `json:"descriptor,omitempty"`
	Line       int               `json:"line,omitempty"`
	Args       json.RawMessage   `json:"args,omitempty"`
	Value      json.RawMessage   `json:"value,omitempty"`
	Error      string            `json:"error,omitempty"`
	DurationNs int64             `json:"durationNs,omitempty"`
	AllocBytes *int64            `json:"allocBytes,omitempty"`
	GCCount    *int64            `json:"gcCount,omitempty"`
	GCPauseMs  *int64            `json:"gcPauseMs,omitempty"`
	Unwound    int               `json:"unwound,omitempty"`
	Truncated  bool              `json:"truncated,omitempty"`
	Dropped    int64             `json:"dropped,omitempty"`
	Resource   map[string]string `json:"resource,omitempty"`
}

func Decode(blob agentclient.Blob, resource map[string]string) ([]Event, error) {
	if blob.Info.ContentType != "application/json" {
		return nil, fmt.Errorf("method events require application/json, received %q", blob.Info.ContentType)
	}
	var events []Event
	if err := json.Unmarshal(blob.Data, &events); err != nil {
		return nil, fmt.Errorf("decode method events: %w", err)
	}
	for i := range events {
		events[i].Timestamp = time.Unix(0, events[i].TS).UTC()
		events[i].Resource = resource
	}
	return events, nil
}

type handler struct{ agent seam.AgentSeam[Params, Event] }

func (handler) Params() Params {
	return Params{Options: Options{Args: true, Return: true, Exception: true, Events: 1000, Expand: 3}}
}
func (handler) Schema() recordresults.ResultType[Event] {
	return recordresults.ResultType[Event]{Title: "JVM method events", TimeColumn: "timestamp", Follow: true, SearchColumns: []string{"class", "method", "thread", "error"}}
}
func (h handler) AgentAvailable(probe string, t target.Target) (bool, error) {
	return h.agent.Available(probe, t)
}
func (h handler) Prepare(ctx dbcontext.Context, p Params, out tracing.Emitter[Event]) (dbcontext.Context, func() error, error) {
	return h.agent.Prepare(ctx, p, out)
}
func (h handler) Handle(ctx dbcontext.Context, p Params, out tracing.Emitter[Event], _ tracing.Records[Event]) error {
	return h.agent.Handle(ctx, p, out)
}
func (h handler) Checkpoint(ctx dbcontext.Context, _ Params, out tracing.Emitter[Event]) error {
	raw, err := checkpointParams(ctx)
	if err != nil {
		return err
	}
	return h.agent.Checkpoint(ctx, raw, out)
}

func NewKind(registry *agentclient.Registry) *tracing.Handler[Params, Event] {
	agent := seam.AgentSeam[Params, Event]{Probe: Kind, Registry: registry, Target: func(p Params) target.Target { return p.Target }, Mutates: func(Params) bool { return true }, ArmParams: func(p Params) (json.RawMessage, error) { return json.Marshal(p.Options) }, DecodeBlob: Decode}
	return tracing.NewHandler[Params, Event](handler{agent: agent}, tracing.Capabilities{Live: true}, target.Agent)
}
