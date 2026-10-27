package exec

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	clickyexec "github.com/flanksource/clicky/exec"
	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/recordstore/recordresults"
	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/seam"
	"github.com/flanksource/telltale/pkg/tracing/target"
	telltale "github.com/flanksource/telltale/sdk"
	clicky "github.com/flanksource/telltale/sdk/clicky"
)

const Kind = "exec"

type Params struct {
	Target    target.Target `json:"target,omitzero"`
	Name      string        `json:"name,omitempty" clicky:"desc=Executable name glob"`
	TailBytes int           `json:"tailBytes" clicky:"desc=Maximum bytes of each output tail"`
}

func (p Params) Validate() error {
	if _, err := filepath.Match(p.Name, ""); err != nil {
		return fmt.Errorf("name glob: %w", err)
	}
	if p.TailBytes < 0 || p.TailBytes > clickyexec.TailBytes {
		return fmt.Errorf("tailBytes must be between 0 and %d", clickyexec.TailBytes)
	}
	return nil
}

type ExecRecord struct {
	tracing.Truncation
	clicky.Record
	Resource map[string]string `json:"resource,omitempty"`
}
type capture struct {
	agent seam.AgentSeam[Params, ExecRecord]
}

func (c capture) AgentAvailable(probe string, t target.Target) (bool, error) {
	return c.agent.Available(probe, t)
}
func (capture) Params() Params { return Params{TailBytes: 4096} }
func (capture) Schema() recordresults.ResultType[ExecRecord] {
	return recordresults.ResultType[ExecRecord]{Title: "Executions", TimeColumn: "startedAt", Follow: true}
}
func (c capture) Prepare(ctx dbcontext.Context, p Params, out tracing.Emitter[ExecRecord]) (dbcontext.Context, func() error, error) {
	return c.agent.Prepare(ctx, p, out)
}
func (c capture) Handle(ctx dbcontext.Context, p Params, out tracing.Emitter[ExecRecord], _ tracing.Records[ExecRecord]) error {
	return c.agent.Handle(ctx, p, maskedArgs{out})
}

// maskedArgs masks secrets passed as separate arguments again on the server,
// so an agent that did not mask them is never stored in the clear.
type maskedArgs struct {
	tracing.Emitter[ExecRecord]
}

func (m maskedArgs) Emit(ctx context.Context, record ExecRecord) error {
	return m.Emitter.Emit(ctx, maskRecordArgs(record))
}

func (m maskedArgs) TryEmit(record ExecRecord) bool {
	return m.Emitter.TryEmit(maskRecordArgs(record))
}

func maskRecordArgs(record ExecRecord) ExecRecord {
	record.Args = telltale.MaskArgs(append([]string{record.Name}, record.Args...))[1:]
	return record
}
func NewKind(registry *agentclient.Registry) *tracing.Handler[Params, ExecRecord] {
	agent := seam.AgentSeam[Params, ExecRecord]{Probe: Kind, Registry: registry, Target: func(p Params) target.Target { return p.Target }, ArmParams: func(p Params) (json.RawMessage, error) { return json.Marshal(p) }}
	return tracing.NewHandler[Params, ExecRecord](capture{agent}, tracing.Capabilities{Live: true}, target.Self, target.Agent).WithSecretMasking().WithTruncation(clickyexec.TailBytes)
}
