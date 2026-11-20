package jmstop

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/commons-db/recordstore/recordresults"
	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/otlp/profiles"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/seam"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

const Kind = "jms_top"

type Params struct {
	Target       target.Target `json:"target,omitzero"`
	Destinations []string      `json:"destinations,omitempty" clicky:"title=Destinations,desc=Only map handlers for these JMS destinations"`
	Poll         string        `json:"poll,omitempty" clicky:"title=Poll,desc=Sampling interval such as 5s"`
	Top          int           `json:"top,omitempty" clicky:"title=Top,desc=Maximum handler mappings per sample; default 50"`
}

func (p Params) Validate() error {
	for _, destination := range p.Destinations {
		if strings.TrimSpace(destination) == "" {
			return fmt.Errorf("destinations must contain nonblank names")
		}
	}
	if p.Top < 0 || p.Top > 10000 {
		return fmt.Errorf("top must be between zero and 10000")
	}
	if p.Poll != "" {
		if d, err := time.ParseDuration(p.Poll); err != nil || d < time.Millisecond {
			return fmt.Errorf("poll must be at least 1ms")
		}
	}
	return nil
}

type Sample struct {
	Source       string            `json:"source" filter:"terms"`
	HandlerID    string            `json:"handlerId"`
	Destination  string            `json:"destination" filter:"terms"`
	Handler      string            `json:"handler" filter:"text"`
	Subscription string            `json:"subscription,omitempty" filter:"terms"`
	State        string            `json:"state" filter:"terms"`
	ActiveCount  int               `json:"activeCount"`
	ElapsedMs    float64           `json:"elapsedMs" pretty:"type=duration,unit=ms"`
	SampledAt    time.Time         `json:"sampledAt"`
	Resource     map[string]string `json:"resource,omitempty"`
}

func ResultType() recordresults.ResultType[Sample] {
	return recordresults.ResultType[Sample]{Kind: Kind, Title: "JMS receive handlers", TimeColumn: "sampledAt", Follow: true, SearchColumns: []string{"destination", "handler", "subscription"}}
}
func FromSnapshot(s profiles.Snapshot) ([]Sample, error) {
	if s.Kind != Kind {
		return nil, fmt.Errorf("JMS top cannot decode %q", s.Kind)
	}
	rows := make([]Sample, 0, len(s.Rows))
	for _, r := range s.Rows {
		data, err := json.Marshal(r.Attributes)
		if err != nil {
			return nil, err
		}
		var row Sample
		if err := json.Unmarshal(data, &row); err != nil {
			return nil, err
		}
		if row.Handler == "" || row.ActiveCount < 0 || (row.State != "idle" && row.State != "busy") {
			return nil, fmt.Errorf("invalid JMS handler mapping %q", r.OperationID)
		}
		row.Source, row.HandlerID, row.SampledAt, row.ElapsedMs = s.Frame().Source, r.OperationID, r.SampledAt, float64(r.ElapsedNS)/1e6
		row.Resource = map[string]string{}
		for k, v := range s.Resource {
			row.Resource[k] = fmt.Sprint(v)
		}
		rows = append(rows, row)
	}
	return rows, nil
}
func decode(b agentclient.Blob, resource map[string]string) (seam.DecodedBlob[Sample], error) {
	if b.Info.ContentType != profiles.ContentType || b.Info.Session == "" || resource["telltale.source.instance"] == "" {
		return seam.DecodedBlob[Sample]{}, fmt.Errorf("JMS top requires authenticated OTLP Profiles and source identity")
	}
	snapshots, err := profiles.Decode(b.Data, profiles.Options{ExpectedKind: Kind, ExpectedCaptureID: b.Info.Session, ExpectedSource: resource["telltale.source.instance"]})
	if err != nil {
		return seam.DecodedBlob[Sample]{}, err
	}
	if len(snapshots) != 1 {
		return seam.DecodedBlob[Sample]{}, fmt.Errorf("JMS top requires one snapshot")
	}
	rows, err := FromSnapshot(snapshots[0])
	frame := snapshots[0].Frame()
	return seam.DecodedBlob[Sample]{Rows: rows, Frame: &frame}, err
}

type handler struct {
	agent seam.AgentSeam[Params, Sample]
}

func (handler) Params() Params                           { return Params{Poll: "5s", Top: 50} }
func (handler) Schema() recordresults.ResultType[Sample] { return ResultType() }
func (h handler) AgentAvailable(p string, t target.Target) (bool, error) {
	return h.agent.Available(p, t)
}
func (h handler) Prepare(c dbcontext.Context, p Params, e tracing.Emitter[Sample]) (dbcontext.Context, func() error, error) {
	return h.agent.Prepare(c, p, e)
}
func (h handler) Handle(c dbcontext.Context, p Params, e tracing.Emitter[Sample], _ tracing.Records[Sample]) error {
	return h.agent.Handle(c, p, e)
}
func (h handler) Checkpoint(c dbcontext.Context, _ Params, e tracing.Emitter[Sample]) error {
	return h.agent.Checkpoint(c, json.RawMessage(`{}`), e)
}
func NewKind(registry *agentclient.Registry) *tracing.Handler[Params, Sample] {
	agent := seam.AgentSeam[Params, Sample]{Probe: Kind, Registry: registry, Target: func(p Params) target.Target { return p.Target }, DecodeBlob: decode, ArmParams: func(p Params) (json.RawMessage, error) {
		if err := p.Validate(); err != nil {
			return nil, err
		}
		d := 5 * time.Second
		if p.Poll != "" {
			d, _ = time.ParseDuration(p.Poll)
		}
		limit := p.Top
		if limit == 0 {
			limit = 50
		}
		return json.Marshal(map[string]any{"pollIntervalMs": d.Milliseconds(), "limit": limit, "destinations": p.Destinations})
	}}
	return tracing.NewHandler[Params, Sample](handler{agent}, tracing.Capabilities{Live: true, Follow: true}, target.Agent).WithTop(tracing.TopSpec{Identity: []string{"source", "handlerId"}, Order: query.Order{{Column: "activeCount", Desc: true}, {Column: "elapsedMs", Desc: true}, {Column: "handler"}}, PerSource: true})
}
