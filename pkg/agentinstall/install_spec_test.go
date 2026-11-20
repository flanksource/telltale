package agentinstall_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/agentinstall"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/jfr"
	"github.com/flanksource/telltale/pkg/tracing/jfr/decode"
	"github.com/flanksource/telltale/pkg/tracing/jvmtrace"
	"github.com/flanksource/telltale/pkg/tracing/tracingtest"
	"github.com/flanksource/telltale/sdk/protocol"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestInstallation(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Agent installation") }

var _ = Describe("Java installation", func() {
	It("attaches to a real JVM, reuses it, and transfers a decodable JFR recording", func() {
		directory := GinkgoT().TempDir()
		output, err := exec.Command("javac", "-d", directory, "testdata/Workload.java").CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(output))
		child := exec.Command("java", "-cp", directory, "Workload")
		Expect(child.Start()).To(Succeed())
		DeferCleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		options := agentinstall.Options{Destination: agentinstall.Destination{Kind: "local"}, PID: child.Process.Pid, Directory: filepath.Join(directory, "agents"), Service: "fixture-jvm", AllowMutation: true}
		installer := agentinstall.Installer{Runtime: agentinstall.Java{}}
		result, err := installer.Install(ctx, options)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Agent).To(Equal(strconv.Itoa(child.Process.Pid)))
		again, err := installer.Install(ctx, options)
		Expect(err).NotTo(HaveOccurred())
		Expect(again).To(Equal(result))
		root, err := os.UserConfigDir()
		Expect(err).NotTo(HaveOccurred())
		registry := agentclient.NewRegistry()
		Expect(registry.Discover(ctx, filepath.Join(root, "telltale", "agents"))).To(Succeed())
		agent := registry.ByPID(child.Process.Pid)
		Expect(agent).NotTo(BeNil())
		DeferCleanup(func() error {
			if agent == nil {
				return nil
			}
			return agent.Close()
		})
		jmx, err := agent.Call(ctx, agentclient.CallRequest{Op: "jvm.jmx.search", Params: json.RawMessage(`{"pattern":"java.lang:type=Runtime"}`)})
		Expect(err).NotTo(HaveOccurred())
		Expect(jmx).To(MatchJSON(`["java.lang:type=Runtime"]`))
		script, err := agent.Call(ctx, agentclient.CallRequest{Op: "jvm.groovy.eval", Params: json.RawMessage(`{"source":"return 3 + 4"}`), AllowMutation: true})
		Expect(err).NotTo(HaveOccurred())
		var evaluated struct {
			Value int `json:"value"`
		}
		Expect(json.Unmarshal(script, &evaluated)).To(Succeed())
		Expect(evaluated.Value).To(Equal(7))
		probe, err := agent.Arm(ctx, agentclient.ArmRequest{Probe: "jvm_trace", AllowMutation: true, Params: json.RawMessage(`{"targets":[{"class":"Workload","method":"compute"}],"args":true,"return":true,"events":10}`)})
		Expect(err).NotTo(HaveOccurred())
		var methodEvents agentclient.Blob
		Eventually(probe.Blobs, 5*time.Second).Should(Receive(&methodEvents))
		Expect(string(methodEvents.Data)).To(ContainSubstring(`"method":"compute"`))
		var ended protocol.Ended
		Eventually(probe.Ended, 5*time.Second).Should(Receive(&ended))
		Expect(ended.Error).To(BeEmpty())
		Expect(ended.Complete).To(BeTrue())
		Expect(probe.Disarm(ctx)).To(Succeed())
		patches, err := agent.Call(ctx, agentclient.CallRequest{Op: "jvm.classes.patches", Params: json.RawMessage(`{}`)})
		Expect(err).NotTo(HaveOccurred())
		Expect(patches).To(MatchJSON(`[]`))
		deadline, err := agent.Arm(ctx, agentclient.ArmRequest{Probe: "jvm_trace", AllowMutation: true, StopAt: time.Now().Add(300 * time.Millisecond), Params: json.RawMessage(`{"targets":[{"class":"Workload","method":"compute"}],"events":1000000}`)})
		Expect(err).NotTo(HaveOccurred())
		Eventually(deadline.Ended, 5*time.Second).Should(Receive(&ended))
		Expect(ended.Error).To(BeEmpty())
		Expect(ended.Complete).To(BeTrue())
		redefined, err := agent.Arm(ctx, agentclient.ArmRequest{Probe: "jvm_redefine", AllowMutation: true, Params: json.RawMessage(`{"class":"Workload","method":"compute","source":"public static double compute(long value) { return 7.0; }"}`)})
		Expect(err).NotTo(HaveOccurred())
		changed, err := agent.Call(ctx, agentclient.CallRequest{Op: "jvm.ognl.eval", AllowMutation: true, Params: json.RawMessage(`{"expression":"@Workload@compute(1L)"}`)})
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(MatchJSON(`{"value":7.0}`))
		Expect(redefined.Disarm(ctx)).To(Succeed())
		restored, err := agent.Call(ctx, agentclient.CallRequest{Op: "jvm.ognl.eval", AllowMutation: true, Params: json.RawMessage(`{"expression":"@Workload@compute(1L)"}`)})
		Expect(err).NotTo(HaveOccurred())
		Expect(restored).To(MatchJSON(`{"value":1.4142135623730951}`))
		env := tracingtest.NewEnv(map[string]tracing.TracePlugin{jvmtrace.RedefineKind: jvmtrace.NewRedefineKind(registry)})
		nativeParams, err := json.Marshal(map[string]any{"target": map[string]any{"seam": "agent", "agent": result.Agent, "allowMutation": true}, "class": "Workload", "method": "compute", "source": "public static double compute(long value) { return 7.0; }"})
		Expect(err).NotTo(HaveOccurred())
		managed, err := env.Runtime.Start(dbcontext.New().Wrap(ctx), tracing.StartRequest{Kind: jvmtrace.RedefineKind, Params: nativeParams})
		Expect(err).NotTo(HaveOccurred())
		tracingtest.Running(managed.Session())
		Expect(env.Runtime.Checkpoint(tracing.WithCheckpointParams(ctx, json.RawMessage(`{"reason":"stop"}`)), managed)).To(Succeed())
		Expect(env.Runtime.Checkpoint(tracing.WithCheckpointParams(ctx, json.RawMessage(`{"unexpected":true}`)), managed)).To(MatchError(tracing.ErrCheckpointParams))
		_, err = managed.Stop(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(env.Rows(tracingtest.Ended(managed.Session()))).To(ContainElement(HaveKeyWithValue("restored", true)))
		session, err := agent.Arm(ctx, agentclient.ArmRequest{Probe: jfr.KindName, Params: json.RawMessage(`{"engine":"jfr","settings":"profile"}`)})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			stop, cancelStop := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancelStop()
			Expect(session.Disarm(stop)).To(Succeed())
		})
		time.Sleep(time.Second)
		checkpoint, err := session.Checkpoint(ctx, json.RawMessage(`{}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(checkpoint.Blobs).To(HaveLen(1))
		profile, err := decode.Decode(checkpoint.Blobs[0].Data, decode.DecodeOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(profile.Samples).NotTo(BeEmpty())
		Expect(session.Disarm(ctx)).To(Succeed())
		_, err = agent.Arm(ctx, agentclient.ArmRequest{Probe: "jvm_redefine", AllowMutation: true, Params: json.RawMessage(`{"class":"Workload","method":"compute","source":"public static double compute(long value) { return 9.0; }"}`)})
		Expect(err).NotTo(HaveOccurred())
		Expect(agent.Close()).To(Succeed())
		agent = nil
		reconnected := agentclient.NewRegistry()
		Expect(reconnected.Discover(ctx, filepath.Join(root, "telltale", "agents"))).To(Succeed())
		againAgent := reconnected.ByPID(child.Process.Pid)
		Expect(againAgent).NotTo(BeNil())
		DeferCleanup(againAgent.Close)
		Eventually(func() (string, error) {
			value, err := againAgent.Call(ctx, agentclient.CallRequest{Op: "jvm.classes.patches", Params: json.RawMessage(`{}`)})
			return string(value), err
		}, 5*time.Second).Should(MatchJSON(`[]`))
	})
})
