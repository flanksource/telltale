package agentclient_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"

	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/sdk"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("agent registry", func() {
	It("uses only verified local discovery for PID lookup", func() {
		dir := GinkgoT().TempDir()
		server, err := telltale.Start(telltale.Options{ConfigDir: dir, Listen: "tcp"})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(server.Close()).To(Succeed()) })
		registry := agentclient.NewRegistry()
		Expect(registry.Discover(context.Background(), filepath.Join(dir, "telltale", "agents"))).To(Succeed())
		agent := registry.ByPID(os.Getpid())
		Expect(agent).NotTo(BeNil())
		DeferCleanup(func() { Expect(agent.Close()).To(Succeed()) })
	})
	It("indexes an agent and notifies subscribers of joins", func() {
		server := telltale.Loopback()
		DeferCleanup(func() { Expect(server.Close()).To(Succeed()) })
		agent, err := agentclient.Connect(context.Background(), server.Connect(), "")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(agent.Close()).To(Succeed()) })
		registry := agentclient.NewRegistry()
		joined, unsubscribe := registry.Subscribe()
		DeferCleanup(unsubscribe)
		Expect(registry.Add(agent)).To(Succeed())
		Expect(joined).To(Receive(Equal(agent)))
		Expect(registry.ByID(strconv.Itoa(os.Getpid()))).To(Equal(agent))
		Expect(registry.ByPID(os.Getpid())).To(BeNil())
		Expect(registry.HasProbe(os.Getpid(), "stacks")).To(BeFalse())
		Expect(registry.HasProbe(os.Getpid(), "missing")).To(BeFalse())
		Expect(registry.Match(map[string]string{"process.pid": strconv.Itoa(os.Getpid())})).To(Equal([]*agentclient.Agent{agent}))
		Expect(registry.Match(map[string]string{"service.name": "absent"})).To(BeEmpty())
	})
})
