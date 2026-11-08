package topui

import (
	"context"
	"time"

	"github.com/flanksource/telltale/pkg/tracing/groups"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("memory columns and pins", func() {
	It("shows compressed memory separately from unavailable swap and pins a quiet recent hog", func() {
		frame := testFrame
		hogs := groups.Hogs{"memory": frame.SampledAt.Add(groups.HogRetention)}
		frame.Groups = []groups.GroupSample{
			sample("busy", "", "busy", groups.KindTool, 100, 100<<20),
			{Key: "hog", Name: "worker", Kind: groups.KindTool, Total: groups.Usage{Processes: 1, CompressedBytes: new(uint64(512 << 20)), Hogs: hogs}},
		}
		m := New(context.Background(), Options{Source: &fakeSource{}, Every: time.Second, Order: groups.Order{By: groups.SortCPU}})
		m.width, m.height = 200, 40
		next, _ := m.Update(frameMsg{frame: frame, ok: true})
		m = next.(Model)
		Expect(m.rows()[0].group.Key).To(Equal("hog"))
		Expect(m.render()).To(SatisfyAll(ContainSubstring("CMPRS"), ContainSubstring("SWAP"), ContainSubstring("512.0M"), ContainSubstring("memory hog")))
		m = press(m, "S")
		Expect(m.rows()[0].group.Key).To(Equal("hog"))
		frame.SampledAt = frame.SampledAt.Add(120 * time.Second)
		next, _ = m.Update(frameMsg{frame: frame, ok: true})
		m = press(next.(Model), "S")
		Expect(m.rows()[0].group.Key).To(Equal("busy"))
	})

	It("orders the selected group's recent hog before a busier process", func() {
		m := New(context.Background(), Options{Every: time.Second, Order: groups.Order{By: groups.SortCPU}})
		m.width, m.height = 200, 40
		m.source = &fakeSource{keeps: true, processes: map[string][]groups.ProcessSample{
			"10": {
				{Key: "busy@1", Usage: groups.Usage{CPUPercent: percent(100)}},
				{Key: "hog@1", Usage: groups.Usage{Hogs: groups.Hogs{"swap": testFrame.SampledAt.Add(groups.HogRetention)}}},
			},
		}}
		next, _ := m.Update(frameMsg{frame: testFrame, ok: true})
		processes, kept := next.(Model).selectedProcesses()
		Expect(kept).To(BeTrue())
		Expect(processes[0].Key).To(Equal("hog@1"))
	})
})
