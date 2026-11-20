package groups_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"time"

	"github.com/flanksource/gavel/sdk/status"
	"github.com/flanksource/telltale/pkg/tracing/groups"
	"github.com/flanksource/telltale/pkg/tracing/process"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func publish(cache string, s status.Status) {
	GinkgoHelper()
	data, err := json.Marshal(s)
	Expect(err).NotTo(HaveOccurred())
	Expect(os.MkdirAll(status.Dir(cache), 0o700)).To(Succeed())
	Expect(os.WriteFile(status.Path(cache, s.PID), data, 0o600)).To(Succeed())
}

var (
	acme   = "/src/acme/.git"
	global = func(state string, index int) status.Lease {
		return status.Lease{Pool: "global", Limit: 4, Index: index, State: state}
	}
	project = func(state string, index int) status.Lease {
		return status.Lease{Pool: "project/abc", Project: acme, Limit: 2, Index: index, State: state}
	}
)

var _ = Describe("gavel status in Top", func() {
	It("shows what a gavel group's process publishes, until it stops publishing", func(ctx SpecContext) {
		cache := GinkgoT().TempDir()
		catalog, err := groups.LoadCatalog(writeCatalog("groups:\n  - name: gavel\n    kind: test\n    target: positional\n    match: [{exec: [sleep]}]\n"))
		Expect(err).NotTo(HaveOccurred())
		gavel := exec.Command("sleep", "31")
		Expect(gavel.Start()).To(Succeed())
		DeferCleanup(func() { _ = gavel.Process.Kill(); _ = gavel.Wait() })
		pid := gavel.Process.Pid
		publish(cache, status.Status{PID: pid, Command: "gavel lint",
			Groups: []status.Group{{Name: "Running linters", Kind: status.GroupLint, Summary: status.Summary{Total: 4, Passed: 1, Running: 3}}},
			Slots:  []status.Lease{global(status.LeaseHeld, 0), project(status.LeaseWaiting, -1)},
		})

		top, err := groups.NewTop(ctx, process.NewSampler(), groups.TopOptions{Catalog: catalog, Groups: []string{"gavel"}, GavelCache: cache})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(top.Close)
		var frame groups.Frame
		gavelGroup := func() groups.GroupSample {
			frame, err = top.Tick(ctx)
			Expect(err).NotTo(HaveOccurred())
			for _, group := range frame.Groups {
				if group.PID == pid {
					return group
				}
			}
			Fail("the gavel process has no group")
			return groups.GroupSample{}
		}

		Eventually(gavelGroup).WithTimeout(5 * time.Second).Should(SatisfyAll(
			HaveField("Gavel", HaveValue(HaveField("Command", "gavel lint"))),
			HaveField("Gavel.Groups", ConsistOf(HaveField("Summary.Passed", 1))),
		))
		Expect(frame.Slots).To(Equal([]groups.SlotPool{
			{Name: "global", Limit: 4, Held: 1},
			{Name: "project/abc", Project: acme, Limit: 2, Waiting: 1},
		}))

		publish(cache, status.Status{PID: pid, Command: "gavel lint",
			Groups: []status.Group{{Name: "Running linters", Kind: status.GroupLint, Summary: status.Summary{Total: 4, Passed: 4}}}})
		Eventually(gavelGroup).WithTimeout(5 * time.Second).Should(HaveField("Gavel.Groups", ConsistOf(HaveField("Summary.Passed", 4))))

		Expect(os.Remove(status.Path(cache, pid))).To(Succeed())
		Eventually(gavelGroup).WithTimeout(5 * time.Second).Should(HaveField("Gavel", BeNil()))
	}, SpecTimeout(30*time.Second))
})

var _ = Describe("gavel slot pools", func() {
	It("adds up held and waiting leases per pool across processes, global first", func() {
		pools := groups.SlotPools([]status.Status{
			{PID: 1, Slots: []status.Lease{project(status.LeaseHeld, 0), global(status.LeaseHeld, 0)}},
			{PID: 2, Slots: []status.Lease{project(status.LeaseHeld, 1), global(status.LeaseHeld, 1)}},
			{PID: 3, Slots: []status.Lease{project(status.LeaseWaiting, -1)}},
		})
		Expect(pools).To(Equal([]groups.SlotPool{
			{Name: "global", Limit: 4, Held: 2},
			{Name: "project/abc", Project: acme, Limit: 2, Held: 2, Waiting: 1},
		}))
		Expect(groups.SlotsText(pools)).To(Equal("global [████░░░░] 2/4 · acme [████████] 2/2 +1 waiting"))
	})

	It("says a process's progress in a line: groups, tests, then held and awaited slots", func() {
		text := groups.GavelText(&status.Status{
			Groups: []status.Group{
				{Kind: status.GroupPackages, Summary: status.Summary{Total: 40, Passed: 10, Failed: 2, Running: 4, Pending: 24}},
				{Kind: status.GroupLint, Summary: status.Summary{}},
				{Name: "nested", Summary: status.Summary{Total: 1, Passed: 1}},
			},
			Tests: &status.Summary{Total: 400, Passed: 118, Failed: 2, Pending: 280},
			Slots: []status.Lease{global(status.LeaseHeld, 0), project(status.LeaseWaiting, -1)},
		})
		// packages: 10 passed → 2 cells █, 2 failed → 1 ▓, 4 running → 1 ▒; tests: one ▓ cell for 2 failures in 400.
		Expect(text).To(Equal("packages [██▓▒░░░░] 12/40 ✗2 · nested [████████] 1/1 · tests [██▓░░░░░] 120/400 ✗2 · ●global ⧗acme"))
		Expect(groups.GavelText(nil)).To(BeEmpty())
	})
})
