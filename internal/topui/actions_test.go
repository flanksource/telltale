package topui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/flanksource/telltale/internal/processcontrol"
	"github.com/flanksource/telltale/pkg/tracing/groups"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type controlledSource struct {
	fakeSource
	requests []processcontrol.Request
}

func (s *controlledSource) Control(_ context.Context, request processcontrol.Request) error {
	s.requests = append(s.requests, request)
	return nil
}

var _ = Describe("terminal process actions", func() {
	It("requires confirmation and retains the chosen process identity across refreshes", func() {
		source := &controlledSource{fakeSource: fakeSource{keeps: true, processes: map[string][]groups.ProcessSample{
			"10": {{Key: "100@1", PID: 100, Name: "worker", Command: "worker", Usage: groups.Usage{CPUPercent: percent(201)}}},
		}}}
		m := New(context.Background(), Options{Source: source, Every: time.Second, Order: groups.Order{By: groups.SortCPU}})
		m.width, m.height = 160, 40
		next, _ := m.Update(frameMsg{frame: testFrame, ok: true})
		m = press(next.(Model), "enter", "x")
		Expect(source.requests).To(BeEmpty())
		Expect(m.footer()).To(ContainSubstring("100"))
		m = press(m, "n", "r")
		Expect(source.requests).To(BeEmpty())
		source.processes["10"][0].Key = "100@2"
		next, cmd := m.Update(tea.KeyPressMsg{Text: "y", Code: 'y'})
		Expect(cmd).NotTo(BeNil())
		_, _ = next.(Model).Update(cmd())
		Expect(source.requests).To(Equal([]processcontrol.Request{{Key: "100@1", Action: "renice", Nice: new(10)}}))
	})
	DescribeTable("uses all cores for the strict CPU hog threshold", func(cpu float64, expected bool) {
		m := Model{frame: &testFrame}
		Expect(m.cpuHog(percent(cpu))).To(Equal(expected))
	}, Entry("above", 201.0, true), Entry("exactly", 200.0, false), Entry("one busy core", 100.0, false))
})
