package telltale_test

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/tracing/httpproxy"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("listing runs", func() {
	var workDir, succeeded, failed, unfinished string
	var started time.Time

	finish := func(command string, specs ...telltale.TraceSpec) string {
		run, err := telltale.Start(context.Background(), telltale.StartOptions{WorkDir: workDir, Command: []string{"sh", "-c", command}, Traces: specs})
		Expect(err).ToNot(HaveOccurred())
		_, err = run.Wait()
		Expect(err).ToNot(HaveOccurred())
		return run.ID()
	}

	BeforeEach(func() {
		workDir = GinkgoT().TempDir()
		started = time.Now()
		succeeded = finish("echo ok", telltale.TraceSpec{Kind: httpproxy.Kind})
		failed = finish("exit 4")
		unfinished = "run-1999-01-01T00-00-00Z"
		Expect(os.MkdirAll(filepath.Join(workDir, traces.Dir, unfinished), 0o755)).To(Succeed())
	})

	ids := func(options telltale.ListOptions) []string {
		options.WorkDir = workDir
		runs, err := telltale.List(options)
		Expect(err).ToNot(HaveOccurred())
		var names []string
		for _, run := range runs {
			names = append(names, run.RunID)
		}
		return names
	}

	DescribeTable("filters, newest first",
		func(options func() telltale.ListOptions, expected func() []string) {
			Expect(ids(options())).To(Equal(expected()))
		},
		Entry("every run", func() telltale.ListOptions { return telltale.ListOptions{} },
			func() []string { return []string{failed, succeeded, unfinished} }),
		Entry("by command text, ignoring case", func() telltale.ListOptions { return telltale.ListOptions{Query: "EXIT"} },
			func() []string { return []string{failed} }),
		Entry("by status", func() telltale.ListOptions {
			return telltale.ListOptions{Status: []telltale.Status{telltale.StatusSucceeded, telltale.StatusUnfinished}}
		}, func() []string { return []string{succeeded, unfinished} }),
		Entry("by armed kind", func() telltale.ListOptions { return telltale.ListOptions{Kind: httpproxy.Kind} },
			func() []string { return []string{succeeded} }),
		Entry("by start, leaving out runs with no known start", func() telltale.ListOptions {
			return telltale.ListOptions{Since: started.Add(-time.Second)}
		}, func() []string { return []string{failed, succeeded} }),
		Entry("up to a limit", func() telltale.ListOptions { return telltale.ListOptions{Limit: 1} },
			func() []string { return []string{failed} }),
	)

	It("describes a finished run from its manifest and an unfinished one by why it has nothing", func() {
		run, err := telltale.Get(telltale.GetOptions{WorkDir: workDir, RunID: failed})
		Expect(err).ToNot(HaveOccurred())
		Expect(run.Status).To(Equal(telltale.StatusFailed))
		Expect(*run.ExitCode).To(Equal(4))
		Expect(run.Command).To(Equal([]string{"sh", "-c", "exit 4"}))

		run, err = telltale.Get(telltale.GetOptions{WorkDir: workDir, RunID: unfinished})
		Expect(err).ToNot(HaveOccurred())
		Expect(run).To(Equal(telltale.RunInfo{
			RunID: unfinished, Dir: filepath.Join(workDir, traces.Dir, unfinished), Status: telltale.StatusUnfinished,
			Unavailable: "the run did not finish: it left no run.json",
		}))
	})

	It("refuses a run that is gone and an id that is a path", func() {
		_, err := telltale.Get(telltale.GetOptions{WorkDir: workDir, RunID: "run-2000-01-01T00-00-00Z"})
		Expect(err).To(MatchError(os.ErrNotExist))
		_, err = telltale.Get(telltale.GetOptions{WorkDir: workDir, RunID: "../escape"})
		Expect(err).To(MatchError(ContainSubstring("is not a run id")))
	})

	It("finds nothing in a work dir that never traced a run", func() {
		Expect(telltale.List(telltale.ListOptions{WorkDir: GinkgoT().TempDir()})).To(BeEmpty())
	})
})
