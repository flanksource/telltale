package seam

import (
	"context"
	"encoding/json"
	"time"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/tracing/target"
	"github.com/flanksource/telltale/sdk/protocol"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type completionEmitter struct{ values []string }

func (e *completionEmitter) Emit(_ context.Context, value string) error {
	e.values = append(e.values, value)
	return nil
}
func (e *completionEmitter) TryEmit(value string) bool {
	e.values = append(e.values, value)
	return true
}
func (*completionEmitter) Report(any)                                {}
func (*completionEmitter) Warn(string)                               {}
func (*completionEmitter) Describe(string, ...query.SessionMetadata) {}
func (*completionEmitter) Export(string, string)                     {}

var _ = Describe("agent capture completion", func() {
	DescribeTable("drains records and distinguishes completion from failure", func(end protocol.Ended, failure string) {
		agent := &agentclient.Agent{Hello: protocol.Hello{AgentID: "fixture"}}
		session := &agentclient.Session{Records: make(chan protocol.Records, 1), Ended: make(chan protocol.Ended, 1)}
		session.Records <- protocol.Records{Items: []json.RawMessage{json.RawMessage(`"final event"`)}}
		session.Ended <- end
		state := &capture{sessions: map[*agentclient.Agent]*agentclient.Session{agent: session}, dropped: make(map[*agentclient.Agent]uint64), report: make(map[string]any)}
		base, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		ctx := dbcontext.New().Wrap(base).WithValue(stateKey{}, state)
		s := AgentSeam[target.Target, string]{Target: func(t target.Target) target.Target { return t }}
		out := &completionEmitter{}
		err := s.Handle(ctx, target.Target{Agent: "fixture"}, out)
		if failure == "" {
			Expect(err).NotTo(HaveOccurred())
		} else {
			Expect(err).To(MatchError(ContainSubstring(failure)))
		}
		Expect(out.values).To(Equal([]string{"final event"}))
	},
		Entry("event budget", protocol.Ended{Complete: true}, ""),
		Entry("unexpected stop", protocol.Ended{}, "ended unexpectedly"),
		Entry("failed cleanup", protocol.Ended{Complete: true, Error: "restore failed"}, "restore failed"),
	)
})
