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

	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/agentinstall"
	"github.com/flanksource/telltale/pkg/tracing/jfr"
	"github.com/flanksource/telltale/pkg/tracing/jfr/decode"
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
		options := agentinstall.Options{Destination: agentinstall.Destination{Kind: "local"}, PID: child.Process.Pid, Directory: filepath.Join(directory, "agents"), Service: "fixture-jvm"}
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
		DeferCleanup(agent.Close)
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
	})
})
