package agentinstall_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/agentinstall"
	"github.com/flanksource/telltale/pkg/credentials/apitoken"
	"github.com/flanksource/telltale/pkg/datadir"
	"github.com/flanksource/telltale/sdk/protocol"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type upgradeArtifact struct {
	agentinstall.Java
	data []byte
}

func (a upgradeArtifact) Artifact() (agentinstall.Artifact, error) {
	return agentinstall.Artifact{Name: "telltale-agent.jar", Data: a.data}, nil
}

var _ = Describe("installer upgrades on a live JVM", func() {
	DescribeTable("replaces the local runtime and reconnects its transports", func(withHub bool) {
		workload := startProfilesWorkload()
		directory := GinkgoT().TempDir()
		options := agentinstall.Options{Destination: agentinstall.Destination{Kind: "local"}, PID: workload.pid,
			Directory: directory, JavaHome: workload.javaHome, Service: "installer-upgrade-fixture", AllowMutation: true}
		registry := agentclient.NewRegistry()
		if withHub {
			tokens := agentclient.NewTokenStore([]datadir.ConfigDir{{Name: datadir.ConfigGlobal, Dir: directory}})
			token, err := tokens.Mint("upgrade-fixture", apitoken.ScopeAgent)
			Expect(err).NotTo(HaveOccurred())
			hub := agentclient.NewHub(registry, tokens)
			mux := http.NewServeMux()
			mux.Handle("/api/agents/connect", hub)
			mux.HandleFunc("GET /api/agents", hub.List)
			server := httptest.NewServer(mux)
			DeferCleanup(server.Close)
			options.HubURL, options.TokenFile = server.URL, filepath.Join(directory, "hub-token")
			Expect(os.WriteFile(options.TokenFile, []byte(token.Secret.Value()), 0o600)).To(Succeed())
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		first, err := (agentinstall.Installer{Runtime: agentinstall.Java{}}).Install(ctx, options)
		Expect(err).NotTo(HaveOccurred())
		root, err := os.UserConfigDir()
		Expect(err).NotTo(HaveOccurred())
		path := filepath.Join(root, "telltale", "agents", strconv.Itoa(workload.pid)+".json")
		endpoint, err := protocol.ReadEndpoint(ctx, path)
		Expect(err).NotTo(HaveOccurred())
		old, err := protocol.Dial(ctx, "tcp", endpoint.Addr, endpoint.Token)
		Expect(err).NotTo(HaveOccurred())
		source := old.Hello.Resource["telltale.source.instance"]
		Expect(old.Close()).To(Succeed())
		before, err := agentclient.Dial(ctx, endpoint)
		Expect(err).NotTo(HaveOccurred())
		verifyUpgradeCaptures(ctx, before)
		Expect(before.Close()).To(Succeed())
		data, err := os.ReadFile(versionedAgent(first.Jar, "installer-upgrade-two"))
		Expect(err).NotTo(HaveOccurred())
		installer := agentinstall.Installer{Runtime: upgradeArtifact{data: data}}
		_, err = installer.Install(ctx, options)
		Expect(err).To(MatchError(ContainSubstring("--upgrade")))
		options.Upgrade = true
		second, err := installer.Install(ctx, options)
		Expect(err).NotTo(HaveOccurred())
		Expect(second.Agent).To(Equal(first.Agent))
		Expect(second.SHA256).NotTo(Equal(first.SHA256))
		endpoint, err = protocol.ReadEndpoint(ctx, path)
		Expect(err).NotTo(HaveOccurred())
		current, err := agentclient.Dial(ctx, endpoint)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(current.Close)
		Expect(current.Hello.SDKVersion).To(Equal("installer-upgrade-two"))
		Expect(current.Hello.Resource).To(HaveKeyWithValue("telltale.source.instance", source))
		verifyUpgradeCaptures(ctx, current)
		if withHub {
			Eventually(func() []string {
				var versions []string
				for _, agent := range registry.Match(nil) {
					versions = append(versions, agent.Hello.SDKVersion)
				}
				return versions
			}).WithTimeout(10 * time.Second).Should(Equal([]string{"installer-upgrade-two"}))
		}
	}, Entry("local TCP", false), Entry("local TCP and hub", true))
})

func verifyUpgradeCaptures(ctx context.Context, agent *agentclient.Agent) {
	GinkgoHelper()
	for _, engine := range []string{"jfr", "async-profiler"} {
		params, err := json.Marshal(map[string]string{"engine": engine, "event": "wall", "interval": "1ms"})
		Expect(err).NotTo(HaveOccurred())
		session, err := agent.Arm(ctx, agentclient.ArmRequest{Probe: "jvm_jfr", Params: params})
		Expect(err).NotTo(HaveOccurred(), "start %s", engine)
		checkpoint, err := session.Checkpoint(ctx, json.RawMessage(`{}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(checkpoint.Blobs).To(HaveLen(1))
		Expect(len(checkpoint.Blobs[0].Data)).To(BeNumerically(">", 4))
		Expect(checkpoint.Blobs[0].Data[:4]).To(Equal([]byte{'F', 'L', 'R', 0}))
		Expect(session.Disarm(ctx)).To(Succeed())
	}
	probe, err := agent.Arm(ctx, agentclient.ArmRequest{Probe: "jvm_trace", AllowMutation: true,
		Params: json.RawMessage(`{"targets":[{"class":"ProfilesWorkload","method":"hold"}],"events":10}`)})
	Expect(err).NotTo(HaveOccurred())
	Expect(probe.Disarm(ctx)).To(Succeed())
	patches, err := agent.Call(ctx, agentclient.CallRequest{Op: "jvm.classes.patches", Params: json.RawMessage(`{}`)})
	Expect(err).NotTo(HaveOccurred())
	Expect(patches).To(MatchJSON(`[]`))
}
