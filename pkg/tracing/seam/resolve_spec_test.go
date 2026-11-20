package seam_test

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/tracing/seam"
	"github.com/flanksource/telltale/pkg/tracing/target"
	"github.com/flanksource/telltale/sdk/protocol"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("agent seam resolvers", func() {
	const remote = "remote/orders-0"

	// registryResolving answers ids under remote/ by adding an agent with
	// probe, or by failing with err; resolved counts the answers.
	registryResolving := func(probe string, err error, resolved *int) *agentclient.Registry {
		registry := agentclient.NewRegistry()
		registry.SetResolver("remote/", func(_ context.Context, registry *agentclient.Registry, id string) error {
			*resolved++
			if err != nil {
				return err
			}
			return registry.Add(&agentclient.Agent{Hello: protocol.Hello{AgentID: id, Probes: []protocol.ProbeSpec{{Name: probe}}}})
		})
		return registry
	}

	It("connects a resolver's agent once, without local discovery", func() {
		var resolved int
		s := seam.AgentSeam[struct{}, string]{Probe: "jvm_trace", Dir: filepath.Join(GinkgoT().TempDir(), "absent"), Registry: registryResolving("jvm_trace", nil, &resolved)}
		for range 2 {
			Expect(s.Available("jvm_trace", target.Target{Agent: remote})).To(BeTrue())
		}
		Expect(resolved).To(Equal(1))
		Expect(s.Available("jvm_jfr", target.Target{Agent: remote})).To(BeFalse())
	})

	It("fails the capture when its resolver cannot connect the agent", func() {
		var resolved int
		s := seam.AgentSeam[struct{}, string]{Probe: "jvm_trace", Registry: registryResolving("jvm_trace", errors.New("port-forward refused"), &resolved)}
		_, err := s.Available("jvm_trace", target.Target{Agent: remote})
		Expect(err).To(MatchError(ContainSubstring(`connect agent "remote/orders-0": port-forward refused`)))
	})
})
