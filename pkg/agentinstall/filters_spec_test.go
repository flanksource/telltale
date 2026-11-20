package agentinstall_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/agentinstall"
	"github.com/flanksource/telltale/pkg/tracing/jvm"
	"github.com/flanksource/telltale/pkg/tracing/jvmtrace"
	"github.com/flanksource/telltale/sdk/protocol"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Java diagnostics on a real JVM", func() {
	var agent *agentclient.Agent
	var ctx context.Context

	BeforeEach(func() {
		directory := GinkgoT().TempDir()
		output, err := exec.Command("javac", "-d", directory, "testdata/Workload.java").CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(output))
		child := exec.Command("java", "-cp", directory, "Workload")
		Expect(child.Start()).To(Succeed())
		DeferCleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
		DeferCleanup(cancel)
		options := agentinstall.Options{Destination: agentinstall.Destination{Kind: "local"}, PID: child.Process.Pid, Directory: filepath.Join(directory, "agents"), Service: "fixture-jvm", AllowMutation: true}
		_, err = (agentinstall.Installer{Runtime: agentinstall.Java{}}).Install(ctx, options)
		Expect(err).NotTo(HaveOccurred())
		root, err := os.UserConfigDir()
		Expect(err).NotTo(HaveOccurred())
		registry := agentclient.NewRegistry()
		Expect(registry.Discover(ctx, filepath.Join(root, "telltale", "agents"))).To(Succeed())
		agent = registry.ByPID(child.Process.Pid)
		Expect(agent).NotTo(BeNil())
		DeferCleanup(agent.Close)
	})

	It("publishes only the root calls whose argument matches and reports how many it filtered", func() {
		Expect(agent.Hello.Probes).To(ContainElement(And(
			HaveField("Name", jvmtrace.Kind),
			HaveField("Version", BeNumerically(">=", jvmtrace.FilterProbeAPI)))))
		// Workload's argument passes through a double, so past 2^53 it is only ever
		// even: a trailing 0 matches at every magnitude and still filters most calls.
		probe, err := agent.Arm(ctx, agentclient.ArmRequest{Probe: jvmtrace.Kind, AllowMutation: true, Params: json.RawMessage(
			`{"targets":[{"class":"Workload","method":"compute"}],"args":true,"events":10,"match":[{"on":"arg","index":0,"op":"regex","value":"0$"}]}`)})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = probe.Disarm(context.Background()) })

		var ended protocol.Ended
		Eventually(probe.Ended, 10*time.Second).Should(Receive(&ended))
		Expect(ended.Error).To(BeEmpty())
		Expect(ended.Complete).To(BeTrue(), "the event budget completes the capture")
		var events []jvmtrace.Event
		for len(probe.Blobs) > 0 {
			decoded, err := jvmtrace.Decode(<-probe.Blobs, nil)
			Expect(err).NotTo(HaveOccurred())
			events = append(events, decoded...)
		}
		var enters int
		for _, event := range events {
			if event.Kind != "enter" {
				continue
			}
			enters++
			var args []struct{ Value string }
			Expect(json.Unmarshal(event.Args, &args)).To(Succeed())
			Expect(strings.HasSuffix(args[0].Value, "0")).To(BeTrue(), "an unmatched call was published: %s", event.Args)
		}
		Expect(enters).To(BeNumerically(">", 0))

		Expect(probe.Reports).NotTo(BeEmpty(), "the agent reports what the filter withheld")
		var report protocol.Report
		for len(probe.Reports) > 0 {
			report = <-probe.Reports
		}
		var summary struct{ Filtered, Omitted int64 }
		Expect(json.Unmarshal(report.Summary, &summary)).To(Succeed())
		Expect(summary.Filtered).To(BeNumerically(">", 0))
	})

	It("reads a typed heap histogram through the DiagnosticCommand MBean", func() {
		raw, err := agent.Call(ctx, agentclient.CallRequest{Op: jvm.HeapHistogramOp, Params: json.RawMessage(`{"top":5,"classPattern":"java.lang.*"}`)})
		Expect(err).NotTo(HaveOccurred())
		histogram, err := jvm.DecodeHeapHistogram(raw)
		Expect(err).NotTo(HaveOccurred())
		Expect(histogram.Rows).To(HaveLen(5))
		for i, row := range histogram.Rows {
			Expect(row.ClassName).To(HavePrefix("java.lang."))
			if i > 0 {
				Expect(row.Bytes).To(BeNumerically("<=", histogram.Rows[i-1].Bytes))
			}
		}
		Expect(histogram.Matched).To(BeNumerically(">=", 5))
	})
})
