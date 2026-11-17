package agentclient

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"time"

	"github.com/flanksource/telltale/sdk/protocol"
	. "github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = Describe("agent inventory", func() {
	const target = "deployer/orders/shop/orders-0"
	var registry *Registry
	var resolved int
	BeforeEach(func() {
		registry = NewRegistry()
		registry.Dir = filepath.Join(GinkgoT().TempDir(), "no-local-agents")
		resolved = 0
		registry.SetResolver("deployer/", func(context.Context, *Registry, string) error {
			resolved++
			return errors.New("inventory must not connect targets")
		})
		registry.SetLister("deployer/", func(context.Context) ([]Known, error) {
			return []Known{{ID: target, Runtime: "java", Names: []string{"orders"}, Resource: map[string]string{"k8s.namespace.name": "shop"},
				State: "deployed", Endpoint: "http://orders-0:7070"}}, nil
		})
	})

	It("lists a deployed target with namespace and deployment metadata without connecting it", func() {
		infos, err := registry.Inventory(context.Background())
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		data, err := json.Marshal(infos)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(data).To(gomega.MatchJSON(`[{"agentId":"deployer/orders/shop/orders-0","resource":{"k8s.namespace.name":"shop"},"connected":false,"group":"namespace/shop","known":{"agentId":"deployer/orders/shop/orders-0","runtime":"java","names":["orders"],"resource":{"k8s.namespace.name":"shop"},"state":"deployed","endpoint":"http://orders-0:7070"}}]`))
		gomega.Expect(resolved).To(gomega.BeZero())
	})

	It("prefers a live handshake and lists a connected deployed agent once", func() {
		hello := protocol.Hello{AgentID: target, Service: "orders-live", Runtime: protocol.Runtime{Lang: "java", Version: "21"},
			Started: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Resource: map[string]string{"k8s.namespace.name": "shop"}}
		agent := &Agent{Hello: hello, done: make(chan struct{})}
		gomega.Expect(registry.Add(agent)).To(gomega.Succeed())
		DeferCleanup(func() { close(agent.done) })
		infos, err := registry.Inventory(context.Background())
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(infos).To(gomega.Equal([]Info{{Hello: &hello, AgentID: target, Resource: map[string]string{"k8s.namespace.name": "shop"}, Connected: true, Group: "namespace/shop"}}))
		gomega.Expect(resolved).To(gomega.BeZero())
	})

	It("reports lister failures instead of returning an empty list", func() {
		registry.SetLister("deployer/", func(context.Context) ([]Known, error) { return nil, errors.New("deployer store is unreadable") })
		_, err := registry.Inventory(context.Background())
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("deployer store is unreadable")))
	})

	DescribeTable("rejects invalid known identities",
		func(known []Known) {
			registry.SetLister("deployer/", func(context.Context) ([]Known, error) { return known, nil })
			_, err := registry.Inventory(context.Background())
			gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("empty or duplicate id")))
		},
		Entry("empty id", []Known{{Runtime: "java"}}),
		Entry("duplicate id", []Known{{ID: target}, {ID: target}}),
	)

	DescribeTable("assigns the same group to connected and saved agents",
		func(resource map[string]string, group string) {
			gomega.Expect(ConnectedInfo(protocol.Hello{Resource: resource}).Group).To(gomega.Equal(group))
			registry.SetLister("deployer/", func(context.Context) ([]Known, error) {
				return []Known{{ID: target, Resource: resource}}, nil
			})
			infos, err := registry.Inventory(context.Background())
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(GroupInventory(infos)).To(gomega.HaveKeyWithValue(group, infos))
		},
		Entry("namespace takes priority", map[string]string{"k8s.namespace.name": "shop", "cloud.provider": "aws", "host.name": "worker"}, "namespace/shop"),
		Entry("cloud", map[string]string{"cloud.provider": "aws", "cloud.region": "eu-west-1", "host.name": "worker"}, "cloud/aws/eu-west-1"),
		Entry("host", map[string]string{"host.name": "worker"}, "host/worker"),
		Entry("other", map[string]string{}, "other"),
	)
})
