package telltale

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/tracing/target"
	sdk "github.com/flanksource/telltale/sdk"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("run target resolution", func() {
	It("prefers an agent that advertises the requested probe for the run PID", func() {
		dir := GinkgoT().TempDir()
		server, err := sdk.Start(sdk.Options{ConfigDir: dir, Listen: "tcp"})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(server.Close()).To(Succeed()) })
		Expect(agentclient.Default.Discover(context.Background(), filepath.Join(dir, "telltale", "agents"))).To(Succeed())
		DeferCleanup(func() {
			if agent := agentclient.Default.ByPID(os.Getpid()); agent != nil {
				Expect(agent.Close()).To(Succeed())
			}
		})
		result, err := aimedSpecs(StartOptions{PID: os.Getpid(), Traces: []TraceSpec{{Kind: "goprof"}}})
		Expect(err).NotTo(HaveOccurred())
		Expect(result[0].target).To(Equal(target.Agent))
	})
	It("accepts a null target when defaulting the PID", func() {
		got, err := withDefaultPID(json.RawMessage(`{"target":null}`), 123)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(got)).To(MatchJSON(`{"target":{"pid":123}}`))
	})
	It("uses the run PID only for seams that accept it", func() {
		for _, kind := range []string{"process_groups"} {
			result, err := aimedSpecs(StartOptions{PID: 123, Traces: []TraceSpec{{Kind: kind}}})
			Expect(err).NotTo(HaveOccurred())
			Expect(string(result[0].Params)).NotTo(ContainSubstring("pid"))
		}
		result, err := aimedSpecs(StartOptions{PID: 123, Traces: []TraceSpec{{Kind: "process_samples"}}})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(result[0].Params)).To(MatchJSON(`{"target":{"pid":123}}`))
		_, err = aimedSpecs(StartOptions{PID: 123, Traces: []TraceSpec{{Kind: "http"}}})
		Expect(err).To(MatchError(ContainSubstring("no seam available for pid")))
	})
	It("keeps an explicitly selected self seam independent of the run PID", func() {
		registration := traces.Registration{Name: "example", Seams: []target.Seam{target.Self, target.Process}, DefaultSeam: target.Self}
		result, err := resolveRunTarget(registration, target.Target{Seam: target.Self}, 123)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(target.Target{Seam: target.Self}))
	})
})
