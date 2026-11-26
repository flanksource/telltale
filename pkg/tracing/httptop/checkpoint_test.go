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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("HTTP Top checkpoint parameters", func() {
	It("checkpoints the Go agent through the runtime and rejects invalid parameters", func() {
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
		for _, raw := range []string{`{"reason":"stop","unknown":true}`, `{"reason":7}`} {
			err := env.Runtime.Checkpoint(tracing.WithCheckpointParams(context.Background(), json.RawMessage(raw)), capture)
			Expect(err).To(MatchError(ContainSubstring("invalid checkpoint params")))
		}
	})
})
