package agentinstall_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/agentinstall"
	"github.com/flanksource/telltale/pkg/datadir"
	"github.com/flanksource/telltale/pkg/tracing/jfr/decode"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Java hub installation", func() {
	It("installs, authenticates and checkpoints independent JFR sessions through the hub", func() {
		directory := GinkgoT().TempDir()
		destination := agentinstall.Destination{Kind: "local"}
		pid := 1
		if configured := os.Getenv("TELLTALE_JVM_FIXTURE_DESTINATION"); configured != "" {
			Expect(json.Unmarshal([]byte(configured), &destination)).To(Succeed())
		}
		if destination.Kind != "kubernetes" {
			output, err := exec.Command("javac", "-d", directory, "testdata/Workload.java").CombinedOutput()
			Expect(err).NotTo(HaveOccurred(), string(output))
			child := exec.Command("java", "-cp", directory, "Workload")
			Expect(child.Start()).To(Succeed())
			DeferCleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
			pid = child.Process.Pid
		}
		store := agentclient.NewTokenStore([]datadir.ConfigDir{{Name: datadir.ConfigGlobal, Dir: directory}})
		token, err := store.Mint("java-fixture", "agent")
		Expect(err).NotTo(HaveOccurred())
		tokenFile := filepath.Join(directory, "token")
		Expect(os.WriteFile(tokenFile, []byte(token.Secret.Value()), 0600)).To(Succeed())
		registry := agentclient.NewRegistry()
		hub := agentclient.NewHub(registry, store)
		mux := http.NewServeMux()
		mux.Handle("/api/agents/connect", hub)
		mux.HandleFunc("/api/agents", hub.List)
		server := httptest.NewUnstartedServer(mux)
		Expect(server.Listener.Close()).To(Succeed())
		server.Listener, err = net.Listen("tcp", "0.0.0.0:0")
		Expect(err).NotTo(HaveOccurred())
		server.Start()
		DeferCleanup(server.Close)
		host := os.Getenv("TELLTALE_JVM_FIXTURE_HUB_HOST")
		if host == "" {
			host = "127.0.0.1"
		}
		_, port, err := net.SplitHostPort(server.Listener.Addr().String())
		Expect(err).NotTo(HaveOccurred())
		installDirectory := filepath.Join(directory, "remote-agent")
		if destination.Kind == "kubernetes" {
			installDirectory = "/opt/telltale-fixture"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		result, err := (agentinstall.Installer{Runtime: agentinstall.Java{}}).Install(ctx, agentinstall.Options{Destination: destination, PID: pid, Directory: installDirectory, HubURL: "http://" + net.JoinHostPort(host, port), TokenFile: tokenFile, Service: "remote-fixture"})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.SHA256).To(HaveLen(64))
		Eventually(func() int { return len(registry.Match(nil)) }, 10*time.Second).Should(Equal(1))
		agent := registry.Match(nil)[0]
		DeferCleanup(agent.Close)
		Expect(agent.Hello.AgentID).To(HavePrefix("hub/"))
		params := json.RawMessage(`{"engine":"jfr","settings":"profile"}`)
		_, err = agent.Arm(ctx, agentclient.ArmRequest{Probe: "jvm_jfr", Params: params, StopAt: time.Now().Add(-time.Second)})
		Expect(err).To(MatchError(ContainSubstring("deadline already passed")))
		first, err := agent.Arm(ctx, agentclient.ArmRequest{Probe: "jvm_jfr", Params: params})
		Expect(err).NotTo(HaveOccurred())
		second, err := agent.Arm(ctx, agentclient.ArmRequest{Probe: "jvm_jfr", Params: params})
		Expect(err).NotTo(HaveOccurred())
		Expect(first.ID).NotTo(Equal(second.ID))
		time.Sleep(time.Second)
		Expect(first.Disarm(ctx)).To(Succeed())
		var final agentclient.Blob
		Eventually(first.Blobs).Should(Receive(&final))
		Expect(final.Info.Session).To(Equal(first.ID))
		checkpoint, err := second.Checkpoint(ctx, json.RawMessage(`{}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(checkpoint.Blobs).To(HaveLen(1))
		profile, err := decode.Decode(checkpoint.Blobs[0].Data, decode.DecodeOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(profile.Samples).NotTo(BeEmpty())
		Expect(second.Disarm(ctx)).To(Succeed())
		previous := agentclient.Default
		agentclient.Default = registry
		DeferCleanup(func() { agentclient.Default = previous })
		runParams, err := json.Marshal(map[string]any{"target": map[string]any{"seam": "agent", "agent": agent.Hello.AgentID}, "engine": "jfr"})
		Expect(err).NotTo(HaveOccurred())
		run, err := telltale.Start(ctx, telltale.StartOptions{DataDir: filepath.Join(directory, "run"), Duration: 2 * time.Second, Every: time.Second, Traces: []telltale.TraceSpec{{Kind: "jvm_jfr", Params: runParams}}})
		Expect(err).NotTo(HaveOccurred())
		recorded, err := run.Wait()
		Expect(err).NotTo(HaveOccurred())
		Expect(recorded.CheckpointErrors).To(BeEmpty())
		Expect(recorded.Captures).To(ConsistOf(And(HaveField("Kind", "jvm_jfr"), HaveField("Summary", HaveKeyWithValue("count", BeNumerically(">", 0))))))
	})
})
