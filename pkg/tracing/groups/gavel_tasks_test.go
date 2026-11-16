package groups_test

import (
	"github.com/flanksource/gavel/sdk/status"
	"github.com/flanksource/telltale/pkg/tracing/groups"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func parentsOf(frame groups.Frame) map[string]string {
	parents := map[string]string{}
	for _, group := range frame.Groups {
		parents[group.Key] = group.ParentKey
	}
	return parents
}

func groupByKey(frame groups.Frame, key string) groups.GroupSample {
	GinkgoHelper()
	for _, group := range frame.Groups {
		if group.Key == key {
			return group
		}
	}
	Fail("no group " + key)
	return groups.GroupSample{}
}

var _ = Describe("WithGavelTasks", func() {
	usage := func(processes int, rss uint64) groups.Usage { return groups.Usage{Processes: processes, RSSBytes: rss} }
	var frame groups.Frame
	BeforeEach(func() {
		gavel := &status.Status{
			Groups: []status.Group{
				{Name: "Running tests across packages", Kind: status.GroupPackages, Summary: status.Summary{Total: 3, Passed: 1, Running: 2}},
				{Name: "Running linters", Kind: status.GroupLint, Summary: status.Summary{Total: 0}},
			},
			Tests: &status.Summary{Total: 40, Passed: 10, Pending: 30},
			Running: []status.Item{
				{Name: "go test ./pkg/a", Group: "Running tests across packages", PID: 300},
				{Name: "go test ./pkg/b", Group: "Running tests across packages", PID: 400},
				{Name: "prepare", Group: "Unknown group"},
			},
		}
		frame = groups.Frame{Groups: []groups.GroupSample{
			{Key: "agent", Name: "claude", Kind: groups.KindAgent, PID: 100, Self: usage(1, 10)},
			{Key: "gavel", ParentKey: "agent", Name: "gavel", Kind: groups.KindTest, PID: 200, Gavel: gavel, SkipAncestry: true, Self: usage(1, 20)},
			{Key: "go-a", ParentKey: "gavel", Name: "go-test", Kind: groups.KindTest, PID: 300, Self: usage(2, 300)},
			{Key: "go-c", ParentKey: "gavel", Name: "go-test", Kind: groups.KindTest, PID: 500, Self: usage(1, 50)},
			{Key: "launchd", Name: "launchd", Kind: groups.KindOther, PID: 1, Self: usage(1, 1)},
			{Key: "go-b", ParentKey: "launchd", Name: "go-test", Kind: groups.KindTest, PID: 400, Self: usage(1, 40)},
		}}
	})

	It("nests task groups, then running tasks, under the gavel, and each task's process group under its task, detached ones included", func() {
		nested := frame.WithGavelTasks()
		Expect(parentsOf(nested)).To(Equal(map[string]string{
			"agent": "", "launchd": "",
			"gavel":                  "agent",
			"gavel/gavel/packages":   "gavel",
			"gavel/gavel/tests":      "gavel",
			"gavel/gavel/packages/0": "gavel/gavel/packages",
			"gavel/gavel/packages/1": "gavel/gavel/packages",
			"gavel/2":                "gavel",
			"go-a":                   "gavel/gavel/packages/0",
			"go-b":                   "gavel/gavel/packages/1",
			"go-c":                   "gavel",
		}), "an empty lint group has no row; a task of an unknown group sits under the gavel; go-c, which no task claims, stays")
		Expect(groupByKey(nested, "gavel/gavel/packages").Total).To(Equal(usage(3, 340)), "the claimed groups' use counts once, under their tasks")
		Expect(groupByKey(nested, "gavel").Total).To(Equal(usage(5, 410)))
		Expect(groupByKey(nested, "launchd").Total).To(Equal(usage(1, 1)), "the detached go-b moved under its task")
		Expect(groupByKey(nested, "gavel/gavel/packages").Task.Summary).To(HaveValue(HaveField("Passed", 1)))
		Expect(groupByKey(nested, "gavel/gavel/packages/1").Task.Item).To(HaveValue(HaveField("PID", 400)))
	})

	It("keeps the tasks of a gavel that is not skip-listed from claiming groups", func() {
		frame.Groups[1].SkipAncestry = false
		parents := parentsOf(frame.WithGavelTasks())
		Expect(parents).To(HaveKeyWithValue("go-a", "gavel"))
		Expect(parents).To(HaveKeyWithValue("go-b", "launchd"))
		Expect(parents).To(HaveKeyWithValue("gavel/gavel/packages/0", "gavel/gavel/packages"))
	})

	It("never lets a task claim its gavel or the gavel's ancestors", func() {
		frame.Groups[1].Gavel.Running[0].PID = 100
		frame.Groups[1].Gavel.Running[1].PID = 200
		parents := parentsOf(frame.WithGavelTasks())
		Expect(parents).To(HaveKeyWithValue("agent", ""))
		Expect(parents).To(HaveKeyWithValue("gavel", "agent"))
	})

	It("leaves a frame without gavel status as it is, and composes after a grouping", func() {
		frame.Groups[1].Gavel = nil
		Expect(frame.WithGavelTasks()).To(Equal(frame))

		frame.Groups[1].Gavel = &status.Status{Running: []status.Item{{Name: "go test ./pkg/a", PID: 300}}}
		for _, by := range []groups.GroupBy{groups.GroupRepo, groups.GroupSlots} {
			projected, err := frame.Grouped(by)
			Expect(err).NotTo(HaveOccurred())
			Expect(parentsOf(projected.WithGavelTasks())).To(HaveKeyWithValue("go-a", "gavel/0"), "grouped by %s", by)
		}
	})
})

var _ = DescribeTable("ShortCommand drops the executable's directory",
	func(command, short string) { Expect(groups.ShortCommand(command)).To(Equal(short)) },
	Entry("a program on a path", "/opt/homebrew/bin/golangci-lint run ./...", "golangci-lint run ./..."),
	Entry("an app bundle's helper, whose name holds spaces",
		"/Applications/Acme Browser.app/Contents/Frameworks/Acme.framework/Helpers/Acme Helper (Renderer).app/Contents/MacOS/Acme Helper (Renderer) --type=renderer",
		"Acme Helper (Renderer) --type=renderer"),
	Entry("a bare program", "gavel test ./...", "gavel test ./..."),
)
