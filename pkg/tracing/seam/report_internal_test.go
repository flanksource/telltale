package seam

import (
	"encoding/json"

	"github.com/flanksource/telltale/pkg/agentclient"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("agent reports", func() {
	It("retains the kind summary when dropped counts change", func() {
		state := &capture{dropped: make(map[*agentclient.Agent]uint64), report: make(map[string]any)}
		agent := &agentclient.Agent{}
		var latest any
		write := func(value any) { latest = value }
		Expect(state.mergeReport(json.RawMessage(`{"captured":12,"dropped":2}`), write)).To(Succeed())
		state.reportDropped(agent, 3, write)
		Expect(latest).To(Equal(map[string]any{"captured": float64(12), "dropped": float64(5)}))
	})
})
