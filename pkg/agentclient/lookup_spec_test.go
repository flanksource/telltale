package agentclient_test

import (
	"context"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/sdk/protocol"
)

var _ = Describe("agent lookup", func() {
	const deployed = "deployer/checkout/shop/checkout-7d9f-x2kq"

	java := func(id, service string, resource map[string]string) *agentclient.Agent {
		return &agentclient.Agent{Hello: protocol.Hello{AgentID: id, Service: service, Runtime: protocol.Runtime{Lang: "java"}, Resource: resource,
			Probes: []protocol.ProbeSpec{{Name: "jvm_trace"}, {Name: "jvm_jfr"}}}}
	}
	registryWith := func(agents ...*agentclient.Agent) *agentclient.Registry {
		registry := agentclient.NewRegistry()
		registry.Dir = filepath.Join(GinkgoT().TempDir(), "no-local-agents")
		for _, agent := range agents {
			Expect(registry.Add(agent)).To(Succeed())
		}
		return registry
	}
	// withDeployed lets the registry list and connect one deployed target.
	withDeployed := func(registry *agentclient.Registry) *agentclient.Registry {
		registry.SetResolver("deployer/", func(_ context.Context, registry *agentclient.Registry, id string) error {
			return registry.Add(java(id, "checkout", map[string]string{"k8s.pod.name": "checkout-7d9f-x2kq", "telltale.deployer": "checkout"}))
		})
		registry.SetLister("deployer/", func(context.Context) ([]agentclient.Known, error) {
			return []agentclient.Known{{ID: deployed, Runtime: "java", Names: []string{"checkout", "checkout-7d9f-x2kq", "shop/checkout-7d9f-x2kq"}}}, nil
		})
		return registry
	}
	lookup := func(registry *agentclient.Registry, query string, want agentclient.Want) (string, error) {
		agent, err := registry.Lookup(context.Background(), query, want)
		if err != nil {
			return "", err
		}
		return agent.Hello.AgentID, nil
	}

	It("picks the only agent there is when none is named", func() {
		Expect(lookup(registryWith(java("52234", "idle-smoke", nil)), "", agentclient.Want{})).To(Equal("52234"))
	})

	It("picks the only agent that offers the probe when none is named", func() {
		goAgent := &agentclient.Agent{Hello: protocol.Hello{AgentID: "a-7f3c2e", Service: "gavel", Runtime: protocol.Runtime{Lang: "go"}, Probes: []protocol.ProbeSpec{{Name: "http"}}}}
		Expect(lookup(registryWith(goAgent, java("52234", "idle-smoke", nil)), "", agentclient.Want{Probe: "jvm_trace"})).To(Equal("52234"))
	})

	It("connects the only deployed target when none is named and nothing is connected", func() {
		Expect(lookup(withDeployed(registryWith()), "", agentclient.Want{Runtime: "java"})).To(Equal(deployed))
	})

	DescribeTable("finds one agent by a short name",
		func(query, want string) {
			registry := withDeployed(registryWith(java("52234", "idle-smoke", map[string]string{"process.pid": "52234"}), java("java/orders-1", "orders", nil)))
			Expect(lookup(registry, query, agentclient.Want{Runtime: "java"})).To(Equal(want))
		},
		Entry("its exact id", "java/orders-1", "java/orders-1"),
		Entry("its service", "idle-smoke", "52234"),
		Entry("the last part of its id", "orders-1", "java/orders-1"),
		Entry("a deployed target's deployer, connecting it", "checkout", deployed),
		Entry("a deployed target's pod", "checkout-7d9f-x2kq", deployed),
		Entry("a unique prefix of a name", "idle", "52234"),
		Entry("a unique prefix of a pod name", "checkout-7d", deployed),
	)

	It("prefers an exact name over agents it is only a prefix of", func() {
		registry := registryWith(java("java/orders", "orders", nil), java("java/orders-batch", "orders-batch", nil))
		Expect(lookup(registry, "orders", agentclient.Want{})).To(Equal("java/orders"))
	})

	It("names every candidate when a short name matches several", func() {
		registry := registryWith(java("java/orders-1", "orders", nil), java("java/orders-2", "orders", nil))
		_, err := lookup(registry, "orders", agentclient.Want{})
		Expect(err).To(MatchError(`agent "orders" matches 2 agents: java/orders-1 (orders), java/orders-2 (orders); name one by its id`))
	})

	It("asks for an agent when several are connected and none is named", func() {
		registry := registryWith(java("java/orders-1", "orders", nil), java("java/billing-1", "billing", nil))
		_, err := lookup(registry, "", agentclient.Want{Runtime: "java"})
		Expect(err).To(MatchError(`2 java agents are connected: java/billing-1 (billing), java/orders-1 (orders); name one with --agent`))
	})

	It("says what it looked for when nothing matches", func() {
		_, err := lookup(registryWith(java("52234", "idle-smoke", nil)), "billing", agentclient.Want{Probe: "jvm_trace"})
		Expect(err).To(MatchError(`no agent offering jvm_trace matches "billing"; agents: 52234 (idle-smoke)`))
		_, err = lookup(registryWith(), "", agentclient.Want{Runtime: "java"})
		Expect(err).To(MatchError("no java agent is connected or deployed: attach one with telltale agents install java, or deploy one with telltale jvm deploy"))
	})
})
