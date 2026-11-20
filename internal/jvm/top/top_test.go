package top

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/tracing/jvm/jvmruntime"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTop(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "JVM Top") }

var _ = Describe("JVM Top", func() {
	It("decodes both operations and preserves unavailable CPU", func() {
		frame, err := Sample(context.Background(), "fixture", func(_ context.Context, request agentclient.CallRequest) (json.RawMessage, error) {
			return os.ReadFile("../../../pkg/tracing/jvm/jvmruntime/testdata/" + request.Op + ".json")
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(frame.AgentID).To(Equal("fixture"))
		Expect(frame.Threads.Threads).NotTo(BeEmpty())
		Expect(frame.Metrics.Memory.Heap).NotTo(BeNil())
	})
	It("rejects malformed agent responses", func() {
		_, err := Sample(context.Background(), "fixture", func(context.Context, agentclient.CallRequest) (json.RawMessage, error) {
			return json.RawMessage(`{}`), nil
		})
		Expect(err).To(HaveOccurred())
	})
	It("propagates cancellation and operation failures without making an empty frame", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := Sample(ctx, "fixture", func(ctx context.Context, _ agentclient.CallRequest) (json.RawMessage, error) { return nil, ctx.Err() })
		Expect(err).To(MatchError(ContainSubstring("context canceled")))
	})
	It("rejects failed and incomplete remote snapshots", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			Expect(json.NewEncoder(w).Encode(map[string]any{"success": true, "output": "{}"})).To(Succeed())
		}))
		defer server.Close()
		source := Source{Server: server.URL}
		_, err := source.Next(context.Background())
		Expect(err).To(MatchError(ContainSubstring("incomplete")))
		_, err = source.Stack(context.Background(), 0)
		Expect(err).To(MatchError(ContainSubstring("positive")))
	})
	It("sorts available CPU first with deterministic ties", func() {
		cpu := 25.0
		rows := []jvmruntime.Thread{{ID: 3}, {ID: 2, CPUPercent: &cpu}, {ID: 1, CPUPercent: &cpu}}
		Expect(Order(rows, "cpu", false, "")).To(Equal([]jvmruntime.Thread{rows[2], rows[1], rows[0]}))
		Expect(Order(rows, "cpu", true, "3")).To(Equal(rows[:1]))
	})
})
