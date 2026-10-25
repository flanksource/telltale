package groups_test

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/flanksource/telltale/pkg/tracing/groups"
	"github.com/flanksource/telltale/pkg/tracing/process"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Top", func() {
	It("reads a live tool's group with its context and usage, rating its CPU from the second tick", func(ctx SpecContext) {
		dir := GinkgoT().TempDir()
		writeFile(filepath.Join(dir, ".git", "HEAD"), "ref: refs/heads/acme\n")
		catalog, err := groups.LoadCatalog(writeCatalog("groups:\n  - name: sleeper\n    kind: tool\n    target: positional\n    match: [{exec: [sleep]}]\n"))
		Expect(err).NotTo(HaveOccurred())
		sleeper := exec.Command("sleep", "30")
		sleeper.Dir = dir
		Expect(sleeper.Start()).To(Succeed())
		DeferCleanup(func() { _ = sleeper.Process.Kill(); _ = sleeper.Wait() })

		top, err := groups.NewTop(ctx, process.NewSampler(), groups.Options{Catalog: catalog, Groups: []string{"sleeper"}})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(top.Close)

		first, err := top.Tick(ctx)
		Expect(err).NotTo(HaveOccurred())
		time.Sleep(1100 * time.Millisecond)
		second, err := top.Tick(ctx)
		Expect(err).NotTo(HaveOccurred())

		Expect(first.Groups).To(ContainElement(HaveField("PID", sleeper.Process.Pid)))
		Expect(second.Groups).To(ContainElement(SatisfyAll(
			HaveField("PID", sleeper.Process.Pid),
			HaveField("Name", "sleeper"),
			HaveField("Target", "30"),
			HaveField("Context.Repo", resolved(dir)),
			HaveField("Context.Branch", "acme"),
			HaveField("Self.Processes", 1),
			HaveField("Self.CPUPercent", Not(BeNil())),
			HaveField("Self.RSSBytes", BeNumerically(">", 0)),
			HaveField("Self.Sockets", HaveValue(BeZero())),
			HaveField("Origin.Scope", groups.ScopeUser),
			HaveField("Origin.Owner", Not(BeEmpty())),
		)))
		Expect(second.Host).To(HaveValue(And(
			HaveField("Cores", BeNumerically(">", 0)),
			HaveField("CPUSystemPercent", Not(BeNil())),
			HaveField("MemoryTotalBytes", BeNumerically(">", 0)),
			HaveField("DiskUsage.ReadOps", Not(BeNil())),
			HaveField("DiskUsage.WriteBps", Not(BeNil())),
		)), "the second tick rates the host's CPU")
		Expect(first.Overhead).To(HaveValue(And(HaveField("CPUPercent", BeNil()), HaveField("TickMillis", BeNumerically(">", 0)))))
		Expect(second.Overhead).To(HaveValue(And(
			HaveField("CPUPercent", HaveValue(BeNumerically(">", 0))),
			HaveField("TickMillis", BeNumerically(">", 0)),
		)), "the ps and lsof each tick runs cost CPU")

		byPID := top.Processes(groups.ProcessFilter{PID: sleeper.Process.Pid})
		Expect(byPID).To(ConsistOf(And(HaveField("PID", sleeper.Process.Pid), HaveField("Name", "sleep"), HaveField("Processes", 1))))
		Expect(top.Processes(groups.ProcessFilter{Name: "sleep"})).To(ContainElement(HaveField("PID", sleeper.Process.Pid)))
		Expect(top.Processes(groups.ProcessFilter{GroupKey: byPID[0].GroupKey})).To(Equal(byPID))
	})

	It("shows the telltale reading the host as its own tool group", func(ctx SpecContext) {
		catalog, err := groups.LoadCatalog()
		Expect(err).NotTo(HaveOccurred())
		top, err := groups.NewTop(ctx, process.NewSampler(), groups.Options{Catalog: catalog})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(top.Close)

		frame, err := top.Tick(ctx)

		Expect(err).NotTo(HaveOccurred())
		Expect(frame.Groups).To(ContainElement(SatisfyAll(
			HaveField("Name", groups.SelfGroup),
			HaveField("Kind", groups.KindTool),
			HaveField("PID", os.Getpid()),
		)))
	})

	It("reads a listed process's descriptors by facet and its environment with secrets masked", func(ctx SpecContext) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(listener.Close)
		port := listener.Addr().(*net.TCPAddr).Port
		const region, token = "eu-acme-1", "acme-s3cret-value"
		child := exec.Command("sleep", "30")
		child.Env = []string{"ACME_REGION=" + region, "ACME_API_TOKEN=" + token}
		Expect(child.Start()).To(Succeed())
		DeferCleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
		catalog, err := groups.LoadCatalog()
		Expect(err).NotTo(HaveOccurred())
		top, err := groups.NewTop(ctx, process.NewSampler(), groups.Options{Catalog: catalog})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(top.Close)
		_, err = top.Tick(ctx)
		Expect(err).NotTo(HaveOccurred())

		self, err := top.Facets(ctx, os.Getpid())
		Expect(err).NotTo(HaveOccurred())
		Expect(self.Descriptors).To(HaveKeyWithValue(process.FacetPorts, ContainElement(HaveField("Port", port))))
		Expect(self.Descriptors).To(HaveKey(process.FacetOpenFiles))

		sleeper, err := top.Facets(ctx, child.Process.Pid)
		Expect(err).NotTo(HaveOccurred())
		Expect(sleeper.Environment).To(Equal(map[string]string{"ACME_REGION": region, "ACME_API_TOKEN": "****"}))

		_, err = top.Facets(ctx, 1<<22)
		Expect(err).To(MatchError(groups.ErrNotListed))
	})

	It("refuses a group no rule names", func(ctx SpecContext) {
		catalog, err := groups.LoadCatalog()
		Expect(err).NotTo(HaveOccurred())

		_, err = groups.NewTop(ctx, process.NewSampler(), groups.Options{Catalog: catalog, Groups: []string{"nope"}})

		Expect(err).To(MatchError(ContainSubstring(`group "nope"`)))
	})
})

// resolved is dir as a process's cwd reads it, through any symlink such as
// darwin's /var.
func resolved(dir string) string {
	path, err := filepath.EvalSymlinks(dir)
	Expect(err).NotTo(HaveOccurred())
	return path
}
