package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/flanksource/telltale/pkg/agentclient"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/spf13/cobra"
)

var _ = Describe("agent inventory commands", func() {
	const target = "deployer/orders/shop/orders-0"
	var command *cobra.Command
	BeforeEach(func() {
		original := agentclient.Default
		DeferCleanup(func() { agentclient.Default = original })
		agentclient.Default = agentclient.NewRegistry()
		agentclient.Default.Dir = filepath.Join(GinkgoT().TempDir(), "no-local-agents")
		command = NewRoot("test")
		agentclient.Default.SetLister("deployer/", func(context.Context) ([]agentclient.Known, error) {
			return []agentclient.Known{{ID: target, Runtime: "java", Names: []string{"orders"},
				Resource: map[string]string{"k8s.namespace.name": "shop"}, State: "deployed", Endpoint: "http://orders-0:7070"}}, nil
		})
	})

	It("groups an unconnected deployed agent by namespace", func() {
		output := &bytes.Buffer{}
		command.SetOut(output)
		command.SetArgs([]string{"agents", "list", "--format", "json"})
		Expect(command.Execute()).To(Succeed())
		var groups map[string][]agentclient.Info
		Expect(json.Unmarshal(output.Bytes(), &groups)).To(Succeed())
		Expect(groups).To(HaveKey("namespace/shop"))
		Expect(groups["namespace/shop"]).To(ConsistOf(And(HaveField("AgentID", target), HaveField("Connected", false), HaveField("Known.State", "deployed"))))
	})

	It("shows deployment details for an exact known agent id", func() {
		output := &bytes.Buffer{}
		command.SetOut(output)
		command.SetArgs([]string{"agents", "get", target})
		Expect(command.Execute()).To(Succeed())
		var info agentclient.Info
		Expect(json.Unmarshal(output.Bytes(), &info)).To(Succeed())
		Expect(info.AgentID).To(Equal(target))
		Expect(info.Known.Endpoint).To(Equal("http://orders-0:7070"))
	})

	It("pretty prints namespace groups and deployment and connection columns by default", func() {
		output := &bytes.Buffer{}
		command.SetOut(output)
		command.SetArgs([]string{"agents", "list", "--format", "pretty"})
		Expect(command.Execute()).To(Succeed())
		Expect(output.String()).To(And(ContainSubstring("namespace/shop"), ContainSubstring("Agent"),
			ContainSubstring("Connection"), ContainSubstring("Not connected"), ContainSubstring("deployed")))
		Expect(json.Valid(output.Bytes())).To(BeFalse())
	})
})
