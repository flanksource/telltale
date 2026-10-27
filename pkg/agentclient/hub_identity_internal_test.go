package agentclient

import (
	"context"

	telltale "github.com/flanksource/telltale/sdk"
	. "github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = Describe("hub identity", func() {
	It("rejects numeric and locally colliding identities", func() {
		local := telltale.Loopback()
		defer func() { _ = local.Close() }()
		agent, err := Connect(context.Background(), local.Connect(), "")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		defer func() { _ = agent.Close() }()
		r := NewRegistry()
		gomega.Expect(r.addHub(agent)).To(gomega.MatchError(gomega.ContainSubstring("numeric")))
		agent.Hello.AgentID = "local-name"
		gomega.Expect(r.Add(agent)).To(gomega.Succeed())
		gomega.Expect(r.addHub(agent)).To(gomega.MatchError(gomega.ContainSubstring("collision")))
	})
})
