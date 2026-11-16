package httptop_test

import (
	"context"
	"encoding/json"

	dbcontext "github.com/flanksource/commons-db/context"
	_ "github.com/flanksource/commons-db/query/providers/sqlite"
	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/httptop"
	"github.com/flanksource/telltale/pkg/tracing/tracingtest"
	sdk "github.com/flanksource/telltale/sdk"
	"github.com/flanksource/telltale/sdk/protocol"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type checkpointProbe struct{ received chan json.RawMessage }

func (*checkpointProbe) Spec() protocol.ProbeSpec {
	return protocol.ProbeSpec{Name: httptop.Kind, Checkpoints: true}
}

func (*checkpointProbe) Arm(context.Context, json.RawMessage, sdk.Sink) (func() error, error) {
	return func() error { return nil }, nil
}

func (p *checkpointProbe) Checkpoint(_ context.Context, params json.RawMessage, _ sdk.Sink) error {
	p.received <- params
	return nil
}

var _ = Describe("HTTP Top checkpoint parameters", func() {
	It("forwards stop reasons through the runtime and rejects invalid parameters before contacting the agent", func() {
		probe := &checkpointProbe{received: make(chan json.RawMessage, 16)}
		sdk.RegisterProbe(probe)
		server := sdk.Loopback()
		DeferCleanup(func() { Expect(server.Close()).To(Succeed()) })
		agent, err := agentclient.Connect(context.Background(), server.Connect(), "")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(agent.Close()).To(Succeed()) })
		registry := agentclient.NewRegistry()
		registry.Dir = GinkgoT().TempDir()
		Expect(registry.Add(agent)).To(Succeed())
		env := tracingtest.NewEnv(map[string]tracing.TracePlugin{httptop.Kind: httptop.NewKind(registry)})
		params, err := json.Marshal(map[string]any{"target": map[string]any{"seam": "agent", "agent": agent.Hello.AgentID}})
		Expect(err).NotTo(HaveOccurred())
		capture, err := env.Runtime.Start(dbcontext.New(), tracing.StartRequest{Kind: httptop.Kind, Params: params})
		Expect(err).NotTo(HaveOccurred())
		tracingtest.Running(capture.Session())
		checkpoint := tracing.WithCheckpointParams(context.Background(), json.RawMessage(`{"reason":"stop"}`))
		Expect(env.Runtime.Checkpoint(checkpoint, capture)).To(Succeed())
		Eventually(probe.received).Should(Receive(MatchJSON(`{"reason":"stop"}`)))
		for _, raw := range []string{`{"reason":"stop","unknown":true}`, `{"reason":7}`} {
			err := env.Runtime.Checkpoint(tracing.WithCheckpointParams(context.Background(), json.RawMessage(raw)), capture)
			Expect(err).To(MatchError(ContainSubstring("invalid checkpoint params")))
			Expect(probe.received).To(BeEmpty())
		}
	})
})
