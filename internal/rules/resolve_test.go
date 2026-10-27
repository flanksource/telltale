package rules

import (
	"context"
	"github.com/flanksource/telltale/pkg/tracing/groups"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"strings"
	"time"
)

var _ = Describe("monitor group ownership", func() {
	It("includes application descendants outside the bundle and stops at nested bundles", func() {
		snapshot := testSnapshot(time.Now(), new(100.0))
		snapshot.Processes[0].Command = "/Applications/Acme.app/Contents/MacOS/Acme"
		snapshot.Processes = append(snapshot.Processes, groups.ProcessSample{Key: "101@1", PID: 101, PPID: 100, GroupKey: "100@1", Command: "node worker.js", Usage: groups.Usage{CPUPercent: new(50.0)}}, groups.ProcessSample{Key: "102@1", PID: 102, PPID: 101, GroupKey: "100@1", Command: "/Applications/Other.app/Contents/MacOS/Other", Usage: groups.Usage{CPUPercent: new(25.0)}})
		resolved, _, err := Resolve(snapshot, "application")
		Expect(err).NotTo(HaveOccurred())
		Expect(resolved).To(HaveLen(2))
		Expect(resolved[0].Members).To(HaveLen(2))
		Expect(resolved[0].Cores).To(HaveValue(Equal(1.5)))
		Expect(resolved[1].Members).To(HaveLen(1))
	})
	It("resolves a tool's owning session anchor outside its selected worktree", func() {
		snapshot := testSnapshot(time.Now(), new(100.0))
		snapshot.Frame.Groups = append(snapshot.Frame.Groups, groups.GroupSample{Key: "101@1", PID: 101, ParentKey: "100@1", Name: "tests", Context: groups.Context{Repo: "/work/child", GitCommonDir: "/work/child/.git"}})
		snapshot.Processes = append(snapshot.Processes, groups.ProcessSample{Key: "101@1", PID: 101, PPID: 100, GroupKey: "101@1", Usage: groups.Usage{CPUPercent: new(100.0)}})
		resolved, _, err := Resolve(snapshot, "worktree")
		Expect(err).NotTo(HaveOccurred())
		Expect(resolved).To(HaveLen(2))
		Expect(resolved[1].Sessions[0].Anchors).To(Equal([]string{"100@1"}))
	})
	It("reports missing attribution in preview without producing an enforceable scope", func() {
		snapshot := testSnapshot(time.Now(), new(100.0))
		snapshot.Frame.Groups[0].Context = groups.Context{}
		resolved, _, err := Resolve(snapshot, "session")
		Expect(err).NotTo(HaveOccurred())
		Expect(resolved).To(BeEmpty())
		program, err := Compile([]byte(strings.ReplaceAll(timedRule, "cores > 1", "true")))
		Expect(err).NotTo(HaveOccurred())
		preview, err := program.Preview(context.Background(), snapshot)
		Expect(err).NotTo(HaveOccurred())
		Expect(preview).To(HaveLen(1))
		Expect(preview[0].Known).To(BeFalse())
		Expect(preview[0].Error).To(ContainSubstring("missing session attribution"))
	})
	It("refuses invalid capacity, cycles and duplicate process identities", func() {
		snapshot := testSnapshot(time.Now(), new(100.0))
		snapshot.Frame.Host.Cores = 0
		_, _, err := Resolve(snapshot, "session")
		Expect(err).To(HaveOccurred())
		snapshot = testSnapshot(time.Now(), new(100.0))
		snapshot.Frame.Groups[0].ParentKey = snapshot.Frame.Groups[0].Key
		_, _, err = Resolve(snapshot, "tree")
		Expect(err).To(HaveOccurred())
		snapshot = testSnapshot(time.Now(), new(100.0))
		snapshot.Processes = append(snapshot.Processes, snapshot.Processes[0])
		_, _, err = Resolve(snapshot, "tree")
		Expect(err).To(HaveOccurred())
	})
})
