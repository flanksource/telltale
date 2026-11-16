package jms

import (
	"encoding/json"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/recordstore/recordresults"
	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/seam"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

type handler struct{ agent seam.AgentSeam[Params, Row] }

func (handler) Params() Params { return Params{Options: Options{Direction: "both"}} }

func (handler) Schema() recordresults.ResultType[Row] {
	return recordresults.ResultType[Row]{Kind: Kind, Title: "JMS operations", TimeColumn: "startedAt", Follow: true, SearchColumns: []string{"destination", "operation", "error", "handler", "messageId", "correlationId"}}
}

func (h handler) AgentAvailable(probe string, t target.Target) (bool, error) {
	return h.agent.Available(probe, t)
}

func (h handler) Prepare(ctx dbcontext.Context, p Params, out tracing.Emitter[Row]) (dbcontext.Context, func() error, error) {
	return h.agent.Prepare(ctx, p, out)
}

func (h handler) Handle(ctx dbcontext.Context, p Params, out tracing.Emitter[Row], _ tracing.Records[Row]) error {
	return h.agent.Handle(ctx, p, out)
}

func (h handler) Checkpoint(ctx dbcontext.Context, _ Params, out tracing.Emitter[Row]) error {
	var params struct {
		Reason string `json:"reason,omitempty"`
	}
	if _, err := tracing.DecodeCheckpointParams(ctx, &params); err != nil {
		return err
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return h.agent.Checkpoint(ctx, raw, out)
}

func NewKind(registry *agentclient.Registry) *tracing.Handler[Params, Row] {
	agent := seam.AgentSeam[Params, Row]{
		Probe: Kind, Registry: registry, Target: func(p Params) target.Target { return p.Target },
		Mutates:      func(Params) bool { return true },
		ArmParams:    func(p Params) (json.RawMessage, error) { return json.Marshal(p.Options) },
		DecodeRecord: DecodeRecord,
		DecodeBlob: func(blob agentclient.Blob, resource map[string]string) (seam.DecodedBlob[Row], error) {
			rows, err := Decode(blob, resource)
			return seam.DecodedBlob[Row]{Rows: rows}, err
		},
	}
	return tracing.NewHandler[Params, Row](handler{agent}, tracing.Capabilities{Live: true}, target.Agent).
		WithSecretMasking().WithTruncation(1 << 20)
}
