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
	"github.com/flanksource/telltale/sdk/protocol"
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
	// Root-call filters: the agent holds each outermost call on its thread and
	// publishes it, with everything nested in it, only when it qualifies.
	MinCost        string       `json:"minCost,omitempty" clicky:"title=Minimum cost,desc=Keep only root calls that ran at least this long such as 250ms"`
	ExceptionsOnly bool         `json:"exceptionsOnly,omitempty" clicky:"title=Exceptions only,desc=Keep only root calls that threw"`
	Match          []ValueMatch `json:"match,omitempty" clicky:"title=Value matches,desc=Keep only root calls whose rendered argument or return value matches every predicate"`
}

// ValueMatch tests one rendered value of a root call: argument Index, or the
// return value, optionally narrowed by a dotted Path of child labels.
type ValueMatch struct {
	On    MatchOn `json:"on" clicky:"required,title=Subject"`
	Index int     `json:"index" clicky:"title=Argument index"`
	Path  string  `json:"path,omitempty" clicky:"title=Path,desc=Dotted child labels inside the value such as customer.name"`
	Op    MatchOp `json:"op" clicky:"required,title=Operator,desc=regex is a Java regular expression found anywhere in the value"`
	Value string  `json:"value" clicky:"required,title=Value"`
}

// MatchOn is which value of a root call a ValueMatch tests.
type MatchOn string

func (MatchOn) JSONSchema() map[string]any {
	return map[string]any{"type": "string", "enum": []any{"arg", "return"}}
}

// MatchOp is how a ValueMatch compares the rendered value.
type MatchOp string

func (MatchOp) JSONSchema() map[string]any {
	return map[string]any{"type": "string", "enum": []any{"equals", "contains", "regex"}}
}

// FilterProbeAPI is the jvm_trace probe generation that implements MinCost,
// ExceptionsOnly and Match.
const FilterProbeAPI = 10

func (o Options) filtered() bool {
	return o.MinCost != "" || o.ExceptionsOnly || len(o.Match) > 0
}

// CompatibleWith refuses filters an agent advertising spec cannot honour. An
// older agent ignores unknown options, so it would return every call as though
// each had matched.
func (o Options) CompatibleWith(spec protocol.ProbeSpec) error {
	if o.filtered() && spec.Version < FilterProbeAPI {
		return fmt.Errorf("minCost, exceptionsOnly and match need jvm_trace probe API v%d, and the agent implements v%d; its agent jar cannot be replaced while the JVM runs, so restart the JVM to filter calls, or trace without filters", FilterProbeAPI, spec.Version)
	}
	return nil
}

func (o Options) validateFilters() error {
	if o.MinCost != "" {
		cost, err := time.ParseDuration(o.MinCost)
		if err != nil || cost < 0 {
			return fmt.Errorf("minCost must be a non-negative duration such as 250ms, got %q", o.MinCost)
		}
	}
	for _, m := range o.Match {
		if err := m.validate(o); err != nil {
			return err
		}
	}
	return nil
}

func (m ValueMatch) validate(o Options) error {
	switch m.Op {
	case "equals", "contains", "regex":
	default:
		return fmt.Errorf("match op must be equals, contains or regex, got %q", m.Op)
	}
	switch m.On {
	case "arg":
		if !o.Args {
			return fmt.Errorf("a match on arg needs args, or the agent never renders the value it would test")
		}
		if m.Index < 0 {
			return fmt.Errorf("match index must be a non-negative argument position, got %d", m.Index)
		}
	case "return":
		if !o.Return {
			return fmt.Errorf("a match on return needs return, or the agent never renders the value it would test")
		}
		if m.Index != 0 {
			return fmt.Errorf("a match on return takes no index, got %d", m.Index)
		}
	default:
		return fmt.Errorf("match on must be arg or return, got %q", m.On)
	}
	return nil
}

func (o Options) Validate() error {
	if err := o.validateFilters(); err != nil {
		return err
	}
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
	Omitted    int               `json:"omitted,omitempty"`
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
	agent := seam.AgentSeam[Params, Event]{Probe: Kind, Registry: registry, Target: func(p Params) target.Target { return p.Target }, Mutates: func(Params) bool { return true }, ArmParams: func(p Params) (json.RawMessage, error) { return json.Marshal(p.Options) }, Compatible: func(p Params, spec protocol.ProbeSpec) error { return p.CompatibleWith(spec) }, DecodeBlob: func(blob agentclient.Blob, resource map[string]string) (seam.DecodedBlob[Event], error) {
		rows, err := Decode(blob, resource)
		return seam.DecodedBlob[Event]{Rows: rows}, err
	}, SummarizeReports: summarizeMethodReports}
	return tracing.NewHandler[Params, Event](handler{agent: agent}, tracing.Capabilities{Live: true}, target.Agent)
}

func summarizeMethodReports(reports []map[string]any) map[string]any {
	summary := make(map[string]any)
	var known, instrumented bool
	restored := len(reports) > 0
	for _, report := range reports {
		armed, ok := report["instrumented"].(bool)
		known = known || ok
		instrumented = instrumented || armed
		restored = restored && report["restored"] == true
		for _, key := range []string{"probeDropped", "filtered", "omitted"} {
			if count, ok := report[key].(float64); ok {
				previous, _ := summary[key].(float64)
				summary[key] = previous + count
			}
		}
	}
	if known {
		summary["instrumented"] = instrumented
		summary["restored"] = restored
	}
	return summary
}
