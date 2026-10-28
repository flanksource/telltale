package jfr

import (
	"encoding/json"
	"fmt"
	"maps"
	"sync"
	"time"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/recordstore/recordresults"
	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/jfr/decode"
	"github.com/flanksource/telltale/pkg/tracing/seam"
	"github.com/flanksource/telltale/pkg/tracing/target"
	"github.com/flanksource/telltale/sdk/protocol"
)

const KindName = "jvm_jfr"

type builtin struct{ native profiler[Params] }
type agentKey struct{}

func (b builtin) Params() Params                           { return Params{Engine: EngineJFR} }
func (b builtin) Schema() recordresults.ResultType[Sample] { return b.native.Schema() }
func (b builtin) AgentAvailable(probe string, t target.Target) (bool, error) {
	return (seam.AgentSeam[Params, Sample]{}).Available(probe, t)
}

func jfrAgent(p Params) seam.AgentSeam[Params, Sample] {
	var mu sync.Mutex
	watermarks := map[string]time.Time{}
	return seam.AgentSeam[Params, Sample]{Probe: KindName, Target: func(p Params) target.Target { return p.Target }, ArmParams: func(p Params) (json.RawMessage, error) { return json.Marshal(p) }, DecodeBlob: func(blob agentclient.Blob, resource map[string]string) ([]Sample, error) {
		if blob.Info.ContentType != protocol.ContentTypeJFR {
			return nil, fmt.Errorf("JFR content type %q", blob.Info.ContentType)
		}
		mu.Lock()
		defer mu.Unlock()
		profile, err := decode.Decode(blob.Data, decode.DecodeOptions{After: watermarks[blob.Info.Session], Threads: p.Threads, MaxStackDepth: p.MaxStackDepth})
		if err != nil {
			return nil, err
		}
		if profile.Latest.After(watermarks[blob.Info.Session]) {
			watermarks[blob.Info.Session] = profile.Latest
		}
		rows := make([]Sample, len(profile.Samples))
		for i, sample := range profile.Samples {
			rows[i] = Sample{StackSample: sample, Resource: maps.Clone(resource)}
		}
		return rows, nil
	}}
}

func (b builtin) Prepare(ctx dbcontext.Context, p Params, out tracing.Emitter[Sample]) (dbcontext.Context, func() error, error) {
	if p.Target.Seam != target.Agent {
		return b.native.Prepare(ctx, p, out)
	}
	agent := jfrAgent(p)
	prepared, release, err := agent.Prepare(ctx, p, out)
	if err != nil {
		return prepared, release, err
	}
	return prepared.WithValue(agentKey{}, agent), release, err
}
func (b builtin) Handle(ctx dbcontext.Context, p Params, out tracing.Emitter[Sample], store tracing.Records[Sample]) error {
	if p.Target.Seam != target.Agent {
		return b.native.Handle(ctx, p, out, store)
	}
	return ctx.Value(agentKey{}).(seam.AgentSeam[Params, Sample]).Handle(ctx, p, out)
}
func (b builtin) Checkpoint(ctx dbcontext.Context, p Params, out tracing.Emitter[Sample]) error {
	var request struct {
		Reason string `json:"reason,omitempty"`
	}
	if _, err := tracing.DecodeCheckpointParams(ctx, &request); err != nil {
		return err
	}
	if p.Target.Seam != target.Agent {
		return b.native.Checkpoint(ctx, p, out)
	}
	return ctx.Value(agentKey{}).(seam.AgentSeam[Params, Sample]).Checkpoint(ctx, json.RawMessage(`{}`), out)
}
func Kind() *tracing.Handler[Params, Sample] {
	return tracing.NewHandler[Params, Sample](builtin{native: profiler[Params]{recorder: NativeRecorder{}}}, tracing.Capabilities{Live: true}, target.Process, target.Agent).WithTruncation(maxStackBytes)
}
