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

const RedefineKind = "jvm_redefine"

type RedefineOptions struct {
	Class     string `json:"class" clicky:"required,title=Class"`
	Method    string `json:"method,omitempty" clicky:"title=Method,desc=Empty replaces the whole class"`
	Loader    string `json:"loader,omitempty" clicky:"title=Class loader"`
	Source    string `json:"source" clicky:"required,title=Source,type=code"`
	Classpath string `json:"classpath,omitempty" clicky:"title=Compile classpath"`
}

func (o RedefineOptions) Validate() error {
	if o.Class == "" || o.Source == "" {
		return fmt.Errorf("class and replacement source are required")
	}
	return nil
}

type RedefineParams struct {
	Target target.Target `json:"target,omitzero"`
	RedefineOptions
}

func (p RedefineParams) Validate() error { return p.RedefineOptions.Validate() }

type Patch struct {
	Timestamp      time.Time `json:"timestamp"`
	Class          string    `json:"class"`
	Loader         string    `json:"loader"`
	Applied        bool      `json:"applied"`
	Restored       bool      `json:"restored"`
	Digest         string    `json:"digest,omitempty"`
	OriginalDigest string    `json:"originalDigest,omitempty"`
}
type redefine struct {
	agent seam.AgentSeam[RedefineParams, Patch]
}

func (redefine) Params() RedefineParams { return RedefineParams{} }
func (redefine) Schema() recordresults.ResultType[Patch] {
	return recordresults.ResultType[Patch]{Title: "JVM redefinitions", TimeColumn: "timestamp", Follow: true}
}
func (h redefine) AgentAvailable(probe string, t target.Target) (bool, error) {
	return h.agent.Available(probe, t)
}
func (h redefine) Prepare(ctx dbcontext.Context, p RedefineParams, out tracing.Emitter[Patch]) (dbcontext.Context, func() error, error) {
	return h.agent.Prepare(ctx, p, out)
}
func (h redefine) Handle(ctx dbcontext.Context, p RedefineParams, out tracing.Emitter[Patch], _ tracing.Records[Patch]) error {
	return h.agent.Handle(ctx, p, out)
}
func (h redefine) Checkpoint(ctx dbcontext.Context, _ RedefineParams, out tracing.Emitter[Patch]) error {
	raw, err := checkpointParams(ctx)
	if err != nil {
		return err
	}
	return h.agent.Checkpoint(ctx, raw, out)
}
func NewRedefineKind(registry *agentclient.Registry) *tracing.Handler[RedefineParams, Patch] {
	agent := seam.AgentSeam[RedefineParams, Patch]{Probe: RedefineKind, Registry: registry, Target: func(p RedefineParams) target.Target { return p.Target }, Mutates: func(RedefineParams) bool { return true }, ArmParams: func(p RedefineParams) (json.RawMessage, error) { return json.Marshal(p.RedefineOptions) }, DecodeBlob: func(b agentclient.Blob, r map[string]string) (seam.DecodedBlob[Patch], error) {
		var rows []Patch
		err := json.Unmarshal(b.Data, &rows)
		return seam.DecodedBlob[Patch]{Rows: rows}, err
	}}
	return tracing.NewHandler[RedefineParams, Patch](redefine{agent: agent}, tracing.Capabilities{Live: true}, target.Agent)
}
