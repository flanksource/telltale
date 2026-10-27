package topui

import (
	"context"
	"encoding/json"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/flanksource/telltale/pkg/tracing/groups"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Model grouping", func() {
	It("cycles modes from raw frames, folds buckets and lists their member processes", func() {
		data, err := os.ReadFile("../../testdata/top-grouping.json")
		Expect(err).NotTo(HaveOccurred())
		var fixture struct {
			Frame groups.Frame `json:"frame"`
		}
		Expect(json.Unmarshal(data, &fixture)).To(Succeed())
		source := &fakeSource{keeps: true, processes: map[string][]groups.ProcessSample{
			"agent-a": {{PID: 1}}, "tests": {{PID: 2}, {PID: 3}}, "agent-b": {{PID: 4}},
		}}
		m := New(context.Background(), Options{Source: source, Every: time.Second, Order: groups.Order{By: groups.SortCPU}})
		next, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 40})
		next, _ = next.(Model).Update(frameMsg{frame: fixture.Frame, ok: true})
		m = press(next.(Model), "b")
		Expect(m.frame.Groups).To(ContainElement(HaveField("Kind", groups.KindBucket)))
		m.selected = "bucket:repo:/work/acme/.git"
		processes, kept := m.selectedProcesses()
		Expect(kept).To(BeTrue())
		Expect(processes).To(HaveLen(4))
		m = press(m, "/", "vitest", "enter")
		Expect(m.rows()[0].group.Total.CPUPercent).To(HaveValue(Equal(float64(30))))
		processes, kept = m.selectedProcesses()
		Expect(kept).To(BeTrue())
		Expect(processes).To(HaveLen(3))
		m = press(m, "esc")
		m = press(m, "left")
		Expect(shown(m)).NotTo(ContainElement("agent-a"))
		m = press(m, "right", "b", "b", "b")
		Expect(m.frame.Groups).To(HaveLen(len(fixture.Frame.Groups)))
		Expect(m.frame.Groups).NotTo(ContainElement(HaveField("Kind", groups.KindBucket)))
	})
})
