package rules

import (
	"context"
	"fmt"
	"time"

	"github.com/flanksource/telltale/pkg/tracing/groups"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func concurrencySnapshot(family string, jobs int) groups.Snapshot {
	snapshot := testSnapshot(time.Now().UTC(), nil)
	snapshot.Frame.Groups, snapshot.Processes = nil, nil
	for index := range jobs {
		pid := (index + 1) * 100
		key := fmt.Sprintf("%d@1", pid)
		snapshot.Frame.Groups = append(snapshot.Frame.Groups,
			groups.GroupSample{Key: key, PID: pid, Name: family, Context: groups.Context{Repo: fmt.Sprintf("/work/project-%d", index)}},
			groups.GroupSample{Key: fmt.Sprintf("%d@1", pid+2), PID: pid + 2, Name: "nested-helper", ParentKey: key})
		for offset := range 3 {
			groupKey := key
			parent := pid
			if offset == 0 {
				parent = 1
			}
			if offset == 2 {
				groupKey = fmt.Sprintf("%d@1", pid+2)
			}
			snapshot.Processes = append(snapshot.Processes, groups.ProcessSample{Key: fmt.Sprintf("%d@1", pid+offset), PID: pid + offset, PPID: parent, GroupKey: groupKey, Name: family, Usage: groups.Usage{RSSBytes: 1024}})
		}
	}
	snapshot.Frame.Groups = append(snapshot.Frame.Groups, groups.GroupSample{Key: "999@1", PID: 999, Name: "unrelated"})
	snapshot.Processes = append(snapshot.Processes, groups.ProcessSample{Key: "999@1", PID: 999, GroupKey: "999@1", Usage: groups.Usage{RSSBytes: 1024}})
	return snapshot
}

var _ = Describe("example concurrency policies", func() {
	It("shares the Go concurrency budget between builds and tests", func() {
		program, err := Load("../../examples/concurrency-rules.yaml")
		Expect(err).NotTo(HaveOccurred())
		snapshot := concurrencySnapshot("go-build", 3)
		snapshot.Frame.Groups[4].Name = "go-test"
		preview, err := program.Preview(context.Background(), snapshot)
		Expect(err).NotTo(HaveOccurred())
		Expect(preview[2].Rule).To(Equal("go-build-concurrency"))
		Expect(preview[2].Matched).To(BeTrue())
		Expect(preview[2].Steps[0].Targets.Processes).To(ConsistOf(
			HaveField("PID", 300), HaveField("PID", 301), HaveField("PID", 302)))
	})
	DescribeTable("counts independent jobs across repositories and targets only excess job trees", func(family, ruleName string, limit int) {
		program, err := Load("../../examples/concurrency-rules.yaml")
		Expect(err).NotTo(HaveOccurred())
		for _, jobs := range []int{limit, limit + 1} {
			snapshot := concurrencySnapshot(family, jobs)
			preview, err := program.Preview(context.Background(), snapshot)
			Expect(err).NotTo(HaveOccurred())
			Expect(preview).To(HaveLen(3))
			for _, item := range preview {
				Expect(item.Error).To(BeEmpty())
				Expect(item.Known).To(BeTrue())
				Expect(item.Matched).To(Equal(item.Rule == ruleName && jobs > limit))
				if !item.Matched {
					continue
				}
				Expect(item.Group.Key).To(Equal("host"))
				Expect(item.Group.Members).To(HaveLen(jobs*3 + 1))
				Expect(item.Steps).To(HaveLen(2))
				for _, step := range item.Steps {
					Expect(step.Error).To(BeEmpty())
					pids := []int{}
					for _, p := range step.Targets.Processes {
						pids = append(pids, p.PID)
					}
					root := (limit + 1) * 100
					Expect(pids).To(ConsistOf(root, root+1, root+2))
				}
			}
		}
	}, Entry("browser sessions", "agent-browser", "agent-browser-concurrency", 4), Entry("Vitest runs", "vitest", "vitest-concurrency", 2), Entry("Go builds", "go-build", "go-build-concurrency", 2), Entry("Go tests", "go-test", "go-build-concurrency", 2))
})
