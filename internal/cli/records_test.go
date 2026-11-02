package cli_test

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/flanksource/telltale/internal/cli"
	"github.com/flanksource/telltale/internal/otelreceiver"
	sdk "github.com/flanksource/telltale/sdk"
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
		_, _, err := execute(append([]string{"records", "process_samples", "--data-dir", GinkgoT().TempDir()}, args...)...)
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
	It("reads, filters and aggregates the receiver pseudo-run's spans", func() {
		dataDir := GinkgoT().TempDir()
		store, err := otelreceiver.OpenStore(otelreceiver.StoreOptions{DataDir: dataDir, Retention: time.Hour, Writable: true})
		Expect(err).NotTo(HaveOccurred())
		at := time.Now().UTC().Add(-time.Minute)
		received := []sdk.Span{
			{TraceID: "trace-a", SpanID: "root", Service: "checkout", Operation: "order", StartedAt: at, DurationMs: 120, Digest: "root", Source: sdk.SpanSource{Type: sdk.SpanSourceOTLP}},
			{TraceID: "trace-a", SpanID: "child", ParentID: "root", Service: "payments", Operation: "charge", StartedAt: at, DurationMs: 40, Digest: "child", Source: sdk.SpanSource{Type: sdk.SpanSourceOTLP}},
		}
		for i := range received {
			Expect(received[i].Normalize()).To(Succeed())
		}
		_, err = store.Persist(context.Background(), received)
		Expect(err).NotTo(HaveOccurred())
		Expect(store.Close()).To(Succeed())
		stdout, stderr, err := execute("records", "spans", "--data-dir", dataDir, "--run", otelreceiver.SourceID, "--filter", "service=payments", "--format", "ndjson")
		Expect(err).NotTo(HaveOccurred(), stderr)
		Expect(strings.Split(strings.TrimSpace(stdout), "\n")).To(ConsistOf(SatisfyAll(ContainSubstring(`"operation":"charge"`), ContainSubstring(`"runId":"receiver"`))))
		stdout, stderr, err = execute("records", "spans", "--data-dir", dataDir, "--run", otelreceiver.SourceID, "--stats", "durationMs", "--group-by", "operation", "--format", "ndjson")
		Expect(err).NotTo(HaveOccurred(), stderr)
		Expect(stdout).To(SatisfyAll(ContainSubstring(`"order"`), ContainSubstring(`"charge"`)))
		root := cli.NewRoot("dev")
		root.SetArgs([]string{"trace", "list", "--data-dir", dataDir, "--json"})
		listed := captureStdout(func() { Expect(root.Execute()).To(Succeed()) })
		Expect(listed).To(MatchJSON(`[{"runId":"receiver","dir":"` + filepath.Join(dataDir, "otel") + `","status":"standing","captures":[{"kind":"spans","ref":{"kind":"spans","stream":"received"}}],"standing":true}]`))
	})
	It("includes runnable examples and window semantics in help", func() {
		page := help("records")
		Expect(page).To(ContainSubstring("telltale records process_samples"))
		Expect(page).To(ContainSubstring("--from overrides"))
	})
})
