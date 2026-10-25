package groups_test

import (
	"github.com/flanksource/telltale/pkg/tracing/groups"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func keys(frame groups.Frame) []string {
	result := make([]string, 0, len(frame.Groups))
	for _, group := range frame.Groups {
		result = append(result, group.Key)
	}
	return result
}

var _ = Describe("Frame.Sorted", func() {
	sample := func(key, parent string, cpu float64, rss uint64) groups.GroupSample {
		return groups.GroupSample{Key: key, Name: key, ParentKey: parent, Total: groups.Usage{CPUPercent: float(cpu), RSSBytes: rss}}
	}
	frame := groups.Frame{Groups: []groups.GroupSample{
		sample("claude-a", "", 10, 900),
		sample("gavel", "claude-a", 2, 100),
		sample("vitest", "claude-a", 8, 50),
		sample("claude-b", "", 40, 10),
		sample("orphan", "gone", 20, 500),
	}}

	DescribeTable("sorts siblings, keeping each group under its parent",
		func(order groups.Order, expected []string) {
			Expect(frame.Sorted(order)).To(WithTransform(keys, Equal(expected)))
		},
		Entry("by CPU, highest first", groups.Order{By: groups.SortCPU}, []string{"claude-b", "orphan", "claude-a", "vitest", "gavel"}),
		Entry("by memory, highest first", groups.Order{By: groups.SortMemory}, []string{"claude-a", "gavel", "vitest", "orphan", "claude-b"}),
		Entry("by CPU, lowest first", groups.Order{By: groups.SortCPU, Reverse: true}, []string{"claude-a", "gavel", "vitest", "orphan", "claude-b"}),
		Entry("by name, A first", groups.Order{By: groups.SortName}, []string{"claude-a", "gavel", "vitest", "claude-b", "orphan"}),
	)

	It("refuses an order it does not know", func() {
		_, err := frame.Sorted(groups.Order{By: "disk"})
		Expect(err).To(MatchError(ContainSubstring(`sort "disk"`)))
	})
})

var _ = DescribeTable("ContextText names a group's session and cmux workspace where it can, else shows their short ids",
	func(context groups.Context, expected string) {
		Expect(groups.ContextText(context)).To(Equal(expected))
	},
	Entry("named", groups.Context{Repo: "/work/acme-api", Branch: "main", Agent: "claude", Session: "5f0c3a52-acme", SessionTitle: "Fix the flaky acme login test", Workspace: "8E1C7A10-ACME", WorkspaceTitle: "acme-api"},
		`acme-api@main claude:"Fix the flaky acme login test" cmux:"acme-api"`),
	Entry("unnamed", groups.Context{Agent: "codex", Session: "019a2b3c-acme", Workspace: "8E1C7A10-ACME"},
		"codex:019a2b3… cmux:8E1C7A1…"),
)
