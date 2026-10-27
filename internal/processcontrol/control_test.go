package processcontrol_test

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	clickyprocess "github.com/flanksource/clicky/process"
	"github.com/flanksource/telltale/internal/processcontrol"
	"github.com/flanksource/telltale/pkg/tracing/process"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestControl(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Process control") }

var _ = Describe("process control", func() {
	DescribeTable("rejects invalid requests before acting", func(request processcontrol.Request) {
		Expect(processcontrol.Apply(context.Background(), request)).NotTo(Succeed())
	},
		Entry("init", processcontrol.Request{Key: "1@1", Action: "terminate"}),
		Entry("no identity", processcontrol.Request{Key: "123", Action: "terminate"}),
		Entry("unknown action", processcontrol.Request{Key: "123@1", Action: "kill"}),
		Entry("no nice", processcontrol.Request{Key: "123@1", Action: "renice"}),
		Entry("invalid nice", processcontrol.Request{Key: "123@1", Action: "renice", Nice: new(20)}),
	)
	It("refuses a stale identity, renices and terminates only the identified child", func() {
		child := exec.Command("sleep", "60")
		Expect(child.Start()).To(Succeed())
		DeferCleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
		snapshot, err := clickyprocess.Discover(context.Background(), clickyprocess.SnapshotOptions{})
		Expect(err).NotTo(HaveOccurred())
		p, ok := snapshot.Get(child.Process.Pid)
		Expect(ok).To(BeTrue())
		key := process.Key(p)
		Expect(processcontrol.Apply(context.Background(), processcontrol.Request{Key: key + "0", Action: "terminate"})).NotTo(Succeed())
		Expect(processcontrol.Apply(context.Background(), processcontrol.Request{Key: key, Action: "renice", Nice: new(10)})).To(Succeed())
		nice, err := exec.Command("ps", "-o", "ni=", "-p", strconv.Itoa(p.PID)).Output()
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.TrimSpace(string(nice))).To(Equal("10"))
		Expect(processcontrol.Apply(context.Background(), processcontrol.Request{Key: key, Action: "terminate"})).To(Succeed())
		Expect(child.Wait()).NotTo(Succeed())
	})
})
