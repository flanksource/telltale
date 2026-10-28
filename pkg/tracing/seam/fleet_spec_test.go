package seam_test

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"time"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/tracing/seam"
	"github.com/flanksource/telltale/pkg/tracing/target"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type fleetRow struct {
	Message  string            `json:"message"`
	Resource map[string]string `json:"resource"`
}
type fleetEmitter struct{ rows chan fleetRow }

func (e *fleetEmitter) Emit(_ context.Context, row fleetRow) error { e.rows <- row; return nil }
func (e *fleetEmitter) TryEmit(row fleetRow) bool                  { e.rows <- row; return true }
func (*fleetEmitter) Warn(string)                                  {}
func (*fleetEmitter) Report(any)                                   {}
func (*fleetEmitter) Describe(string, ...query.SessionMetadata)    {}
func (*fleetEmitter) Export(string, string)                        {}

var _ = Describe("dial-out fleet", func() {
	It("streams records with each pod resource and arms a late joining process", func() {
		store := agentclient.NewTokenStore(GinkgoT().TempDir())
		token, err := store.Mint("fleet-test", "agent")
		Expect(err).NotTo(HaveOccurred())
		registry := testRegistry()
		server := httptest.NewServer(agentclient.NewHub(registry, store))
		DeferCleanup(server.Close)
		start := func(pod string) {
			cmd := exec.Command("go", "run", "-race", "./testdata/hubchild")
			cmd.Env = append(os.Environ(), "TELLTALE_URL="+server.URL, "TELLTALE_TOKEN="+token.Secret.Value(), "POD_NAME="+pod, "AGENT_CONFIG_DIR="+GinkgoT().TempDir())
			cmd.Stderr = GinkgoWriter
			stdin, err := cmd.StdinPipe()
			Expect(err).NotTo(HaveOccurred())
			Expect(cmd.Start()).To(Succeed())
			DeferCleanup(func() { Expect(stdin.Close()).To(Succeed()); Expect(cmd.Wait()).To(Succeed()) })
		}
		start("one")
		start("two")
		Eventually(func() int { return len(registry.Match(nil)) }, 60*time.Second).Should(Equal(2))
		targeting := target.Target{Seam: target.Agent, Selector: map[string]string{"process.runtime.name": "go"}}
		out := &fleetEmitter{rows: make(chan fleetRow, 8)}
		s := seam.AgentSeam[target.Target, fleetRow]{Probe: "fleet-test", Registry: registry, Target: func(t target.Target) target.Target { return t }}
		base, cancel := context.WithCancel(context.Background())
		ctx, release, err := s.Prepare(dbcontext.New().Wrap(base), targeting, out)
		Expect(err).NotTo(HaveOccurred())
		finished := make(chan error, 1)
		go func() { finished <- s.Handle(ctx, targeting, out) }()
		var first, second, third fleetRow
		Eventually(out.rows, 10*time.Second).Should(Receive(&first))
		Eventually(out.rows, 10*time.Second).Should(Receive(&second))
		Expect([]string{first.Resource["k8s.pod.name"], second.Resource["k8s.pod.name"]}).To(ConsistOf("one", "two"))
		start("three")
		Eventually(out.rows, 60*time.Second).Should(Receive(&third))
		Expect(third.Resource["k8s.pod.name"]).To(Equal("three"))
		cancel()
		Eventually(finished).Should(Receive(BeNil()))
		Expect(release()).To(Succeed())
	})
})
