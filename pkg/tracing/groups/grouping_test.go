package groups_test

import (
	"encoding/json"
	"os"

	"github.com/flanksource/telltale/pkg/tracing/groups"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type bucketExpectation struct {
	Target     string   `json:"target"`
	Members    []string `json:"members"`
	Processes  int      `json:"processes"`
	CPUPercent float64  `json:"cpuPercent"`
	RSSBytes   uint64   `json:"rssBytes"`
}

var _ = Describe("Frame.Grouped", func() {
	var frame groups.Frame
	var expected map[groups.GroupBy][]bucketExpectation
	BeforeEach(func() {
		data, err := os.ReadFile("../../../testdata/top-grouping.json")
		Expect(err).NotTo(HaveOccurred())
		var fixture struct {
			Frame    groups.Frame                           `json:"frame"`
			Expected map[groups.GroupBy][]bucketExpectation `json:"expected"`
		}
		Expect(json.Unmarshal(data, &fixture)).To(Succeed())
		frame, expected = fixture.Frame, fixture.Expected
	})

	DescribeTable("projects the shared fixture without counting parent totals twice", func(by groups.GroupBy) {
		projected, err := frame.Grouped(by)
		Expect(err).NotTo(HaveOccurred())
		var actual []bucketExpectation
		for _, bucket := range projected.Groups {
			if bucket.Kind != groups.KindBucket {
				continue
			}
			members := map[string]bool{bucket.Key: true}
			item := bucketExpectation{Target: bucket.Target, Processes: bucket.Total.Processes, CPUPercent: *bucket.Total.CPUPercent, RSSBytes: bucket.Total.RSSBytes}
			for _, group := range projected.Groups {
				if group.Kind != groups.KindBucket && members[group.ParentKey] {
					members[group.Key] = true
					item.Members = append(item.Members, group.Key)
				}
			}
			actual = append(actual, item)
		}
		Expect(actual).To(Equal(expected[by]))
		Expect(frame.Groups[0].Total.CPUPercent).To(HaveValue(Equal(float64(999))))
		Expect(projected.SampledAt).To(Equal(frame.SampledAt))
	}, Entry("repository", groups.GroupRepo), Entry("worktree", groups.GroupWorktree), Entry("Claude session", groups.GroupClaudeSession))

	It("keeps the process tree unchanged and rejects unknown modes", func() {
		Expect(frame.Grouped(groups.GroupTree)).To(Equal(frame))
		_, err := frame.Grouped("invalid")
		Expect(err).To(MatchError(ContainSubstring("group-by")))
	})

	It("preserves unknown measurements and does not invent historical repository identities", func() {
		frame.Groups = []groups.GroupSample{{Key: "old", Name: "go", Kind: groups.KindBuild, PID: 1, Context: groups.Context{Repo: "/work/old"}, Self: groups.Usage{Processes: 1, RSSBytes: 50}}}
		projected, err := frame.Grouped(groups.GroupRepo)
		Expect(err).NotTo(HaveOccurred())
		Expect(projected.Groups).To(HaveLen(2))
		Expect(projected.Groups[0].Name).To(Equal("Unattributed"))
		Expect(projected.Groups[0].Total.CPUPercent).To(BeNil())
	})

	It("keeps bucket keys stable when branches and session titles change", func() {
		for _, by := range []groups.GroupBy{groups.GroupRepo, groups.GroupWorktree, groups.GroupClaudeSession} {
			before, err := frame.Grouped(by)
			Expect(err).NotTo(HaveOccurred())
			frame.Groups[0].Context.Branch = "renamed"
			frame.Groups[0].Context.SessionTitle = "Renamed session"
			after, err := frame.Grouped(by)
			Expect(err).NotTo(HaveOccurred())
			Expect(keys(after)).To(Equal(keys(before)))
		}
	})

	It("retains relationships only within a bucket and re-rates the parent", func() {
		projected, err := frame.Grouped(groups.GroupRepo)
		Expect(err).NotTo(HaveOccurred())
		byKey := map[string]groups.GroupSample{}
		for _, group := range projected.Groups {
			byKey[group.Key] = group
		}
		Expect(byKey["tests"].ParentKey).To(Equal("agent-a"))
		Expect(byKey["codex"].ParentKey).To(Equal("bucket:repo:/work/other/.git"))
		Expect(byKey["agent-a"].Total.CPUPercent).To(HaveValue(Equal(float64(30))))
	})

	It("refuses cyclic and duplicate group identities", func() {
		frame.Groups[0].ParentKey = "tests"
		_, err := frame.Grouped(groups.GroupRepo)
		Expect(err).To(MatchError(ContainSubstring("cycle")))
		frame.Groups[0].ParentKey = ""
		frame.Groups = append(frame.Groups, frame.Groups[0])
		_, err = frame.Grouped(groups.GroupRepo)
		Expect(err).To(MatchError(ContainSubstring("duplicate")))
	})

	It("distinguishes repositories with the same name and sums known optional readings", func() {
		frame.Groups = []groups.GroupSample{frame.Groups[0], frame.Groups[3]}
		frame.Groups[0].Self.NetRx = float(1024)
		frame.Groups[1].Context.GitCommonDir = "/other/acme/.git"
		projected, err := frame.Grouped(groups.GroupRepo)
		Expect(err).NotTo(HaveOccurred())
		Expect(projected.Groups[0].Name).To(Equal(projected.Groups[2].Name))
		Expect(projected.Groups[0].Key).NotTo(Equal(projected.Groups[2].Key))
		Expect(projected.Groups[0].Total.NetRx).To(HaveValue(Equal(float64(1024))))
		Expect(projected.Groups[2].Total.NetRx).To(BeNil())
	})
})
