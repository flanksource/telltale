package telltale_test

import (
	"context"
	"path/filepath"
	"strconv"
	"time"

	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/tracing/goprof"
	"github.com/flanksource/telltale/pkg/tracing/process"
	"github.com/flanksource/telltale/pkg/tracing/stacks"
	"github.com/flanksource/telltale/pkg/tracing/tracingtest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func preset(name string) telltale.Preset {
	for _, preset := range telltale.Presets() {
		if preset.Name == name {
			return preset
		}
	}
	Fail("no preset " + name)
	return telltale.Preset{}
}

var _ = Describe("presets", func() {
	It("registers ps and gops, each with the help its command shows", func() {
		var names []string
		for _, preset := range telltale.Presets() {
			names = append(names, preset.Name)
			Expect(preset.Description).ToNot(BeEmpty(), preset.Name)
			Expect(preset.Examples).ToNot(BeEmpty(), preset.Name)
		}
		Expect(names).To(ContainElements("gops", "ps"))
	})

	DescribeTable("refuses a preset it cannot run",
		func(preset telltale.Preset, refusal string) {
			Expect(telltale.RegisterPreset(preset)).To(MatchError(ContainSubstring(refusal)))
		},
		Entry("a builtin's name", telltale.Preset{Name: "ps", Summary: "again"}, `"ps": it is registered twice`),
		Entry("a kind's name", telltale.Preset{Name: "stacks", Summary: "a kind"}, `"stacks" is a trace kind's name`),
		Entry("an unknown kind", telltale.Preset{Name: "preset_test_unknown", Summary: "nope", Traces: []telltale.TraceSpec{{Kind: "nope"}}}, `unknown trace kind "nope"`),
		Entry("no summary", telltale.Preset{Name: "preset_test_blank"}, "a name and a summary are required"),
	)

	It("ps follows an attached process's tree with every facet", func() {
		target := sleeper("30")
		run, err := preset("ps").Start(context.Background(), telltale.StartOptions{
			WorkDir: GinkgoT().TempDir(), PID: target.Process.Pid, Duration: time.Second,
		})
		Expect(err).ToNot(HaveOccurred())
		result, err := run.Wait()
		Expect(err).ToNot(HaveOccurred())
		Expect(result.PID).To(Equal(target.Process.Pid))
		Expect(result.Process.Tree.Kind).To(Equal(process.TreeKind))
		Expect(result.CheckpointErrors).To(BeEmpty())
	})

	It("gops refuses a process that runs no gops agent, naming the port file it looked for", func() {
		gopsDir := GinkgoT().TempDir()
		GinkgoT().Setenv("GOPS_CONFIG_DIR", gopsDir)
		target := sleeper("30")
		workDir := GinkgoT().TempDir()
		_, err := preset("gops").Start(context.Background(), telltale.StartOptions{WorkDir: workDir, PID: target.Process.Pid})
		Expect(err).To(MatchError(ContainSubstring("runs no gops agent: no " + filepath.Join(gopsDir, strconv.Itoa(target.Process.Pid)))))
		Expect(filepath.Join(workDir, ".telltale")).ToNot(BeADirectory())
	})

	It("gops reads a Go process's goroutines and heap over its agent", func() {
		gopsDir := GinkgoT().TempDir()
		GinkgoT().Setenv("GOPS_CONFIG_DIR", gopsDir)
		child := tracingtest.StartGoChild(tracingtest.GoChildBinary(GinkgoT().TempDir()), "-gops", gopsDir)
		run, err := preset("gops").Start(context.Background(), telltale.StartOptions{
			WorkDir: GinkgoT().TempDir(), PID: child.PID, Duration: 500 * time.Millisecond,
		})
		Expect(err).ToNot(HaveOccurred())
		result, err := run.Wait()
		Expect(err).ToNot(HaveOccurred())
		Expect(result.CheckpointErrors).To(BeEmpty())
		Expect(result.Captures).To(ConsistOf(
			And(HaveField("Kind", stacks.Kind), HaveField("Summary", HaveKeyWithValue("count", BeNumerically(">", 0)))),
			And(HaveField("Kind", goprof.Kind), HaveField("Summary", HaveKeyWithValue("count", BeNumerically(">", 0)))),
		))
	})
})
