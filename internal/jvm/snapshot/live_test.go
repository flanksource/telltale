package snapshot

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/tracing/jfr/decode"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("disposable JVM support snapshot", func() {
	It("attaches by PID, masks properties and arguments, and closes only its own recording", func() {
		directory := GinkgoT().TempDir()
		output, err := exec.Command("javac", "-d", directory, "../../../pkg/agentinstall/testdata/Workload.java").CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(output))
		child := exec.Command("java", "-Dexample.password=snapshot-private-value", "-cp", directory, "Workload")
		Expect(child.Start()).To(Succeed())
		DeferCleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		previous := agentclient.Default
		agentclient.Default = agentclient.NewRegistry()
		DeferCleanup(func() { agentclient.Default = previous })
		id, err := Resolve(ctx, "", child.Process.Pid, "")
		Expect(err).NotTo(HaveOccurred())
		agent := agentclient.Default.ByID(id)
		recording, err := agent.Arm(ctx, agentclient.ArmRequest{Probe: "jvm_jfr", Params: json.RawMessage(`{"engine":"jfr","settings":"profile"}`)})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
			defer done()
			Expect(recording.Disarm(cleanup)).To(Succeed())
			Expect(agent.Close()).To(Succeed())
		})
		result, err := Collect(ctx, Options{Agent: id, JFR: "1s", HeapHistogram: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Manifest.Complete).To(BeTrue(), "%+v", result.Manifest.Collectors)
		Expect(string(result.Files["heap-histogram.txt"])).To(ContainSubstring("java.lang.String"))
		for name, content := range result.Files {
			Expect(bytes.Contains(content, []byte("snapshot-private-value"))).To(BeFalse(), name)
		}
		profile, err := decode.Decode(result.Files["recording.jfr"], decode.DecodeOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(profile.Samples).NotTo(BeEmpty())
		checkpoint, err := recording.Checkpoint(ctx, json.RawMessage(`{}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(checkpoint.Blobs).To(HaveLen(1))
		again, err := Resolve(ctx, "", child.Process.Pid, filepath.Join(directory, "not-a-jdk"))
		Expect(err).NotTo(HaveOccurred())
		Expect(again).To(Equal(id))
	})
})
