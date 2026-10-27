package llm

import (
	"encoding/json"
	"fmt"
	"path"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/recordstore/recordresults"
	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/seam"
	"github.com/flanksource/telltale/pkg/tracing/target"
	captain "github.com/flanksource/telltale/sdk/captain"
)

const Kind = "llm"

type Params struct {
	Target           target.Target `json:"target,omitzero"`
	Models           string        `json:"models,omitempty" clicky:"desc=Model glob"`
	IncludePrompts   bool          `json:"includePrompts,omitempty" clicky:"desc=Include prompts"`
	IncludeResponses bool          `json:"includeResponses,omitempty" clicky:"desc=Include responses"`
}

func (p Params) Validate() error {
	if _, err := path.Match(p.Models, ""); err != nil {
		return fmt.Errorf("models glob: %w", err)
	}
	return nil
}

type LLMCall struct {
	tracing.Truncation
	captain.Record
	Resource map[string]string `json:"resource,omitempty"`
}
type capture struct {
	agent seam.AgentSeam[Params, LLMCall]
}

func (c capture) AgentAvailable(probe string, t target.Target) (bool, error) {
	return c.agent.Available(probe, t)
}
func (capture) Params() Params { return Params{} }
func (capture) Schema() recordresults.ResultType[LLMCall] {
	return recordresults.ResultType[LLMCall]{Title: "LLM calls", Follow: true}
}
func (c capture) Prepare(ctx dbcontext.Context, p Params, out tracing.Emitter[LLMCall]) (dbcontext.Context, func() error, error) {
	return c.agent.Prepare(ctx, p, out)
}
func (c capture) Handle(ctx dbcontext.Context, p Params, out tracing.Emitter[LLMCall], _ tracing.Records[LLMCall]) error {
	return c.agent.Handle(ctx, p, out)
}
func NewKind(registry *agentclient.Registry) *tracing.Handler[Params, LLMCall] {
	agent := seam.AgentSeam[Params, LLMCall]{Probe: Kind, Registry: registry, Target: func(p Params) target.Target { return p.Target }, ArmParams: func(p Params) (json.RawMessage, error) { return json.Marshal(p) }}
	return tracing.NewHandler[Params, LLMCall](capture{agent}, tracing.Capabilities{Live: true}, target.Self, target.Agent).WithTruncation(64<<10).WithJSONProcessor("prompt", "response").WithSecretMasking()
}
