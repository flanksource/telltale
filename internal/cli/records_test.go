package cli_test

import (
	"github.com/flanksource/telltale/internal/cli"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("records command", func() {
	It("registers follow on both run command forms", func() {
		root := cli.NewRoot("test")
		for _, args := range [][]string{{"run", "process_samples"}, {"trace", "run"}} {
			cmd, _, err := root.Find(args)
			Expect(err).NotTo(HaveOccurred())
			Expect(cmd.Flags().Lookup("follow")).NotTo(BeNil())
			Expect(cmd.Flags().Set("follow", "true")).To(Succeed())
		}
	})
	DescribeTable("refuses invalid selections before opening stores", func(args []string, message string) {
		_, _, err := execute(append([]string{"records", "process_samples", "--work-dir", GinkgoT().TempDir()}, args...)...)
		Expect(err).To(MatchError(ContainSubstring(message)))
	},
		Entry("follow across runs", []string{"--runs", "one,two", "--follow"}, "--follow requires a single run"),
		Entry("two selectors", []string{"--run", "one", "--runs", "two"}, "--run and --runs"),
		Entry("negative limit", []string{"--limit", "-1"}, "limit"),
		Entry("bad seq", []string{"--after-seq", "5", "--to-seq", "2"}, "seq"),
		Entry("bad time", []string{"--from", "yesterday-ish"}, "--from"),
		Entry("malformed filter", []string{"--filter", "pid"}, "col=expr"),
		Entry("bad CEL", []string{"--where", "row.cpuPercent +"}, "CEL"),
		Entry("view and stats", []string{"--view", "summary", "--stats", "cpuPercent"}, "--view"),
		Entry("follow aggregates", []string{"--follow", "--stats", "cpuPercent"}, "--follow"),
		Entry("view with explicit zero limit", []string{"--view", "summary", "--limit", "0"}, "--view"),
		Entry("nonpositive since", []string{"--since", "0s"}, "positive"),
		Entry("follow with page cursor", []string{"--follow", "--cursor", "next"}, "--follow"),
	)
	It("includes runnable examples and window semantics in help", func() {
		page := help("records")
		Expect(page).To(ContainSubstring("telltale records process_samples"))
		Expect(page).To(ContainSubstring("--from overrides"))
	})
})
