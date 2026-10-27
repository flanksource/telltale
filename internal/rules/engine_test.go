package rules

import (
	"context"
	"github.com/flanksource/telltale/pkg/tracing/groups"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"time"
)

func testSnapshot(at time.Time, cpu *float64) groups.Snapshot {
	return groups.Snapshot{Frame: groups.Frame{SampledAt: at, Host: &groups.HostUsage{Cores: 8, MemoryTotalBytes: 8 * 1024 * 1024 * 1024}, Groups: []groups.GroupSample{{Key: "100@1", PID: 100, Name: "claude", Kind: "agent", Context: groups.Context{Agent: "claude", Session: "test", Repo: "/work/acme", GitCommonDir: "/work/acme/.git"}}}}, Processes: []groups.ProcessSample{{Key: "100@1", PID: 100, Name: "claude", GroupKey: "100@1", Usage: groups.Usage{CPUPercent: cpu, RSSBytes: 1024}}}}
}

const timedRule = `version: 1
rules:
  - name: cpu
    groupBy: session
    match: cores > 1
    for: 4s
    recover: cores < 0.8
    recoverFor: 2s
    steps:
      - wait: 0s
        hook: {type: renice, targets: group.members, nice: 10}
      - wait: 2s
        hook: {type: signal, targets: group.members, signal: SIGTERM}
`

var _ = Describe("monitoring incidents", func() {
	at := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	DescribeTable("preserves a newer incident when an older hook finishes or remains unresolved", func(finished bool) {
		program, err := Compile([]byte(timedRule))
		Expect(err).NotTo(HaveOccurred())
		old := Incident{ID: "old", Rule: "cpu", Group: "session:claude:test", Revision: program.Revision, State: "breached"}
		current := old
		current.ID = "current"
		current.State = "pending"
		events := []Event{{Kind: "intent", At: at, Incident: old}, {Kind: "state", At: at.Add(time.Second), Incident: current}}
		if finished {
			old.State = "closed"
			events = append(events, Event{Kind: "result", At: at.Add(2 * time.Second), Incident: old, Result: &HookResult{Status: "failed"}})
		}
		engine := NewEngine(program, 2*time.Second)
		engine.Restore(events)
		Expect(engine.States()).To(Equal([]Incident{current}))
	}, Entry("finished", true), Entry("unresolved", false))
	It("normalizes group resources and aggregates repository members once", func() {
		snapshot := testSnapshot(at, new(200.0))
		snapshot.Frame.Groups = append(snapshot.Frame.Groups, groups.GroupSample{Key: "101@1", ParentKey: "100@1", PID: 101, Name: "tests", Context: snapshot.Frame.Groups[0].Context})
		snapshot.Processes = append(snapshot.Processes, groups.ProcessSample{Key: "101@1", PID: 101, GroupKey: "101@1", Usage: groups.Usage{CPUPercent: new(50.0), RSSBytes: 2048}})
		resolved, _, err := Resolve(snapshot, "repo")
		Expect(err).NotTo(HaveOccurred())
		Expect(resolved).To(HaveLen(1))
		Expect(resolved[0].Members).To(HaveLen(2))
		Expect(resolved[0].Cores).To(HaveValue(Equal(2.5)))
		Expect(resolved[0].CPU).To(HaveValue(Equal(31.25)))
		Expect(resolved[0].RSS).To(Equal(int64(3072)))
	})
	It("holds, escalates once, recovers and starts a new incident", func() {
		program, err := Compile([]byte(timedRule))
		Expect(err).NotTo(HaveOccurred())
		engine := NewEngine(program, 2*time.Second)
		advance := func(offset time.Duration, cpu *float64) []Decision {
			d, _, err := engine.Advance(context.Background(), testSnapshot(at.Add(offset), cpu))
			Expect(err).NotTo(HaveOccurred())
			return d
		}
		Expect(advance(0, new(200.0))).To(BeEmpty())
		Expect(advance(2*time.Second, new(200.0))).To(BeEmpty())
		decisions := advance(4*time.Second, new(200.0))
		Expect(decisions).To(HaveLen(1))
		first := decisions[0].Incident.ID
		engine.Complete(decisions[0], at.Add(4*time.Second), HookResult{Status: "succeeded"})
		Expect(advance(5*time.Second, new(200.0))).To(BeEmpty())
		decisions = advance(6*time.Second, new(200.0))
		Expect(decisions).To(HaveLen(1))
		Expect(decisions[0].Hook.Signal).To(Equal("SIGTERM"))
		engine.Complete(decisions[0], at.Add(6*time.Second), HookResult{Status: "succeeded"})
		Expect(advance(8*time.Second, new(200.0))).To(BeEmpty())
		Expect(advance(10*time.Second, new(50.0))).To(BeEmpty())
		Expect(advance(12*time.Second, new(50.0))).To(BeEmpty())
		Expect(advance(14*time.Second, new(200.0))).To(BeEmpty())
		Expect(advance(16*time.Second, new(200.0))).To(BeEmpty())
		decisions = advance(18*time.Second, new(200.0))
		Expect(decisions).To(HaveLen(1))
		Expect(decisions[0].Incident.ID).NotTo(Equal(first))
	})
	It("requires fresh continuity after unknown CPU and sampling gaps", func() {
		program, err := Compile([]byte(timedRule))
		Expect(err).NotTo(HaveOccurred())
		engine := NewEngine(program, 2*time.Second)
		for _, offset := range []time.Duration{0, 2 * time.Second} {
			_, _, err = engine.Advance(context.Background(), testSnapshot(at.Add(offset), new(200.0)))
			Expect(err).NotTo(HaveOccurred())
		}
		d, _, err := engine.Advance(context.Background(), testSnapshot(at.Add(4*time.Second), nil))
		Expect(err).NotTo(HaveOccurred())
		Expect(d).To(BeEmpty())
		d, _, err = engine.Advance(context.Background(), testSnapshot(at.Add(6*time.Second), new(200.0)))
		Expect(err).NotTo(HaveOccurred())
		Expect(d).To(BeEmpty())
		d, _, err = engine.Advance(context.Background(), testSnapshot(at.Add(20*time.Second), new(200.0)))
		Expect(err).NotTo(HaveOccurred())
		Expect(d).To(BeEmpty())
	})
	DescribeTable("rejects invalid rule configuration", func(document string) { _, err := Compile([]byte(document)); Expect(err).To(HaveOccurred()) }, Entry("unknown YAML", timedRule+"extra: true\n"), Entry("wrong version", "version: 2\nrules: []"))
})
