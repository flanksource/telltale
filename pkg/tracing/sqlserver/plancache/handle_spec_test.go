package plancache

import (
	"context"
	"errors"
	"time"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/telltale/pkg/tracing"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type recordingEmitter struct {
	rows    []Sample
	reports []any
	err     error
}

func (e *recordingEmitter) Emit(_ context.Context, s Sample) error {
	if e.err != nil {
		return e.err
	}
	e.rows = append(e.rows, s)
	return nil
}
func (e *recordingEmitter) TryEmit(s Sample) bool                   { return e.Emit(context.Background(), s) == nil }
func (e *recordingEmitter) Report(s any)                            { e.reports = append(e.reports, s) }
func (*recordingEmitter) Warn(string)                               {}
func (*recordingEmitter) Describe(string, ...query.SessionMetadata) {}
func (*recordingEmitter) Export(string, string)                     {}

func scriptedCapture(p Params, snapshots ...[]CachedPlan) (*capture, *int) {
	var results []cacheSnapshot
	for i, plans := range snapshots {
		result := cacheSnapshot{ServerTime: planCreated.Add(time.Duration(i) * time.Second), Plans: plans}
		for _, plan := range plans {
			result.Handles = append(result.Handles, plan.PlanHandle)
		}
		results = append(results, result)
	}
	return scriptedSnapshots(p, results...)
}

func scriptedSnapshots(p Params, snapshots ...cacheSnapshot) (*capture, *int) {
	GinkgoHelper()
	calls := 0
	c := &capture{params: p, now: func() time.Time { return planCreated.Add(time.Duration(calls) * time.Second) }}
	c.reader = func(ctx context.Context, options snapshotOptions) (cacheSnapshot, error) {
		Expect(ctx.Err()).NotTo(HaveOccurred())
		Expect(options.Filter.Database).To(Equal(p.Databases))
		Expect(options.Filter.Text).To(Equal(p.Text))
		if calls >= len(snapshots) {
			return cacheSnapshot{}, errors.New("unexpected extra poll")
		}
		if calls == 0 {
			Expect(options.Since).To(BeZero())
		} else {
			Expect(options.Since).To(Equal(snapshots[calls-1].ServerTime))
		}
		rows := snapshots[calls]
		calls++
		return rows, nil
	}
	return c, &calls
}

var _ = Describe("plan cache capture", func() {
	DescribeTable("rejects invalid snapshot metadata without advancing the watermark", func(invalid cacheSnapshot) {
		c, _ := scriptedSnapshots(Params{Top: 50},
			cacheSnapshot{ServerTime: planCreated, Handles: []string{"0x01"}, Plans: []CachedPlan{testPlan()}}, invalid)
		out := &recordingEmitter{}
		Expect(c.poll(context.Background(), out)).To(Succeed())
		Expect(c.poll(context.Background(), out)).To(HaveOccurred())
		Expect(c.watermark).To(Equal(planCreated))
		Expect(out.rows).To(BeEmpty())
	},
		Entry("missing server time", cacheSnapshot{}),
		Entry("server clock regressed", cacheSnapshot{ServerTime: planCreated.Add(-time.Second)}),
		Entry("empty cache handle", cacheSnapshot{ServerTime: planCreated.Add(time.Second), Handles: []string{""}}),
	)
	It("includes plans created between the membership and active-statistics reads", func() {
		c, _ := scriptedSnapshots(Params{Top: 50},
			cacheSnapshot{ServerTime: planCreated},
			cacheSnapshot{ServerTime: planCreated.Add(time.Second), Plans: []CachedPlan{testPlan()}},
		)
		out := &recordingEmitter{}
		Expect(c.poll(context.Background(), out)).To(Succeed())
		Expect(c.poll(context.Background(), out)).To(Succeed())
		Expect(out.rows).To(HaveLen(1))
		Expect(out.rows[0].Evicted).To(BeFalse())
	})
	It("preserves a quiet plan's baseline until it executes again or disappears from the handle set", func() {
		base, active := testPlan(), testPlan()
		active.ExecutionCount += 2
		active.TotalCPU += 5 * time.Millisecond
		c, _ := scriptedSnapshots(Params{Top: 50},
			cacheSnapshot{ServerTime: planCreated, Handles: []string{base.PlanHandle}, Plans: []CachedPlan{base}},
			cacheSnapshot{ServerTime: planCreated.Add(time.Second), Handles: []string{base.PlanHandle}},
			cacheSnapshot{ServerTime: planCreated.Add(2 * time.Second), Handles: []string{base.PlanHandle}, Plans: []CachedPlan{active}},
			cacheSnapshot{ServerTime: planCreated.Add(3 * time.Second)},
		)
		out := &recordingEmitter{}
		Expect(c.poll(context.Background(), out)).To(Succeed())
		Expect(c.poll(context.Background(), out)).To(Succeed())
		Expect(out.rows).To(BeEmpty(), "absence from active statistics is not eviction")
		Expect(c.poll(context.Background(), out)).To(Succeed())
		Expect(out.rows).To(HaveLen(1))
		Expect(out.rows[0].Executions).To(Equal(int64(2)))
		Expect(out.rows[0].CPUMs).To(Equal(float64(5)))
		Expect(c.poll(context.Background(), out)).To(Succeed())
		Expect(out.rows).To(HaveLen(2))
		Expect(out.rows[1].Evicted).To(BeTrue())
	})
	It("still detects a recompile after quiet polls", func() {
		base, recompiled := testPlan(), testPlan()
		recompiled.CreationTime = planCreated.Add(time.Second)
		recompiled.ExecutionCount = 1
		c, _ := scriptedSnapshots(Params{Top: 50},
			cacheSnapshot{ServerTime: planCreated, Handles: []string{base.PlanHandle}, Plans: []CachedPlan{base}},
			cacheSnapshot{ServerTime: planCreated.Add(time.Second), Handles: []string{base.PlanHandle}},
			cacheSnapshot{ServerTime: planCreated.Add(2 * time.Second), Handles: []string{base.PlanHandle}, Plans: []CachedPlan{recompiled}},
		)
		out := &recordingEmitter{}
		for range 3 {
			Expect(c.poll(context.Background(), out)).To(Succeed())
		}
		Expect(out.rows).To(HaveLen(1))
		Expect(out.rows[0].Recompiled).To(BeTrue())
		Expect(out.rows[0].Executions).To(Equal(int64(1)))
	})
	It("accepts the runtime stop reason and forces a final checkpoint reading", func() {
		base, cur := testPlan(), testPlan()
		cur.ExecutionCount++
		c, calls := scriptedCapture(Params{Top: 50}, []CachedPlan{base}, []CachedPlan{cur})
		out := &recordingEmitter{}
		Expect(c.poll(context.Background(), out)).To(Succeed())
		ctx := dbcontext.NewContext(tracing.WithCheckpointParams(context.Background(), []byte(`{"reason":"stop"}`))).WithValue(captureKey{}, c)
		Expect((handler{}).Checkpoint(ctx, Params{}, out)).To(Succeed())
		Expect(*calls).To(Equal(2))
		Expect(out.rows).To(HaveLen(1))
		Expect(out.rows[0].Executions).To(Equal(int64(1)))
	})
	It("reports a baseline without rows, then checkpoints interval deltas and finalizes on stop", func() {
		base := testPlan()
		cur := base
		cur.ExecutionCount += 2
		cur.TotalCPU += 8 * time.Millisecond
		cur.TotalElapsed += 12 * time.Millisecond
		p := Params{Connection: "connection://example", Poll: "1h", Top: 200}
		c, calls := scriptedCapture(p, []CachedPlan{base}, []CachedPlan{cur}, []CachedPlan{cur})
		out := &recordingEmitter{}
		Expect(c.poll(context.Background(), out)).To(Succeed())
		Expect(out.rows).To(BeEmpty())
		Expect(out.reports).To(HaveLen(1))
		ctx, cancel := context.WithCancel(context.Background())
		prepared := dbcontext.NewContext(ctx).WithValue(captureKey{}, c)
		Expect((handler{}).Checkpoint(prepared, p, out)).To(Succeed())
		Expect(out.rows).To(HaveLen(1))
		Expect(out.rows[0].Executions).To(Equal(int64(2)))
		Expect(out.rows[0].IntervalMs).To(Equal(float64(1000)))
		cancel()
		Expect((handler{}).Handle(prepared, p, out, nil)).To(Succeed())
		Expect(*calls).To(Equal(3))
		Expect(out.rows).To(HaveLen(1))
		Expect(out.reports[len(out.reports)-1].(map[string]any)["polls"]).To(Equal(3))
	})
	It("emits recompiles, new plans and zero-delta eviction rows once", func() {
		base := testPlan()
		recompiled := base
		recompiled.CreationTime = planCreated.Add(time.Second)
		recompiled.ExecutionCount = 1
		recompiled.TotalCPU = time.Millisecond
		fresh := base
		fresh.PlanHandle = "0x02"
		p := (handler{}).Params()
		c, _ := scriptedCapture(p, []CachedPlan{base}, []CachedPlan{recompiled, fresh}, []CachedPlan{fresh}, []CachedPlan{fresh})
		out := &recordingEmitter{}
		for range 4 {
			Expect(c.poll(context.Background(), out)).To(Succeed())
		}
		Expect(out.rows).To(HaveLen(3))
		Expect(out.rows).To(ContainElement(And(HaveField("Recompiled", true), HaveField("Executions", int64(1)))))
		evicted := out.rows[2]
		Expect(evicted.Evicted).To(BeTrue())
		Expect(evicted.PlanHandle).To(Equal(base.PlanHandle))
		Expect(evicted.Executions).To(BeZero())
		Expect(evicted.CPUMs).To(BeZero())
		Expect(evicted.TotalExecutions).To(Equal(int64(1)))
	})
	It("ranks by interval CPU, retains omitted baselines and applies the interval execution floor", func() {
		oldBusy, newBusy := testPlan(), testPlan()
		oldBusy.PlanHandle = "0x02"
		oldBusy.TotalCPU = time.Hour
		curOld, curNew := oldBusy, newBusy
		curOld.ExecutionCount++
		curOld.TotalCPU += time.Millisecond
		curNew.ExecutionCount += 3
		curNew.TotalCPU += 10 * time.Millisecond
		p := Params{Top: 1, MinExecutions: 2}
		c, _ := scriptedCapture(p, []CachedPlan{oldBusy, newBusy}, []CachedPlan{curOld, curNew}, []CachedPlan{curOld, curNew})
		out := &recordingEmitter{}
		for range 3 {
			Expect(c.poll(context.Background(), out)).To(Succeed())
		}
		Expect(out.rows).To(HaveLen(1))
		Expect(out.rows[0].PlanHandle).To(Equal(newBusy.PlanHandle))
		Expect(out.rows[0].CPUMs).To(Equal(float64(10)))
	})
	It("cuts Top after computing deltas and never treats dropped plans as evicted", func() {
		first, second := testPlan(), testPlan()
		second.PlanHandle = "0x02"
		curFirst, curSecond := first, second
		curFirst.ExecutionCount++
		curFirst.TotalCPU += time.Millisecond
		curSecond.ExecutionCount++
		curSecond.TotalCPU += 2 * time.Millisecond
		c, _ := scriptedCapture(Params{Top: 1}, []CachedPlan{first, second}, []CachedPlan{curFirst, curSecond}, []CachedPlan{curFirst, curSecond})
		out := &recordingEmitter{}
		for range 3 {
			Expect(c.poll(context.Background(), out)).To(Succeed())
		}
		Expect(out.rows).To(HaveLen(1))
		Expect(out.rows[0].PlanHandle).To(Equal(second.PlanHandle))
	})
	It("surfaces reader, emission and counter errors", func() {
		failure := errors.New("read failed")
		c := &capture{reader: func(context.Context, snapshotOptions) (cacheSnapshot, error) { return cacheSnapshot{}, failure }, now: time.Now}
		Expect(c.poll(context.Background(), &recordingEmitter{})).To(MatchError(failure))
		base, invalid := testPlan(), testPlan()
		invalid.TotalCPU--
		c, _ = scriptedCapture(Params{Top: 1}, []CachedPlan{base}, []CachedPlan{invalid})
		Expect(c.poll(context.Background(), &recordingEmitter{})).To(Succeed())
		Expect(c.poll(context.Background(), &recordingEmitter{})).To(HaveOccurred())
		c, _ = scriptedCapture(Params{Top: 1}, nil, []CachedPlan{base})
		Expect(c.poll(context.Background(), &recordingEmitter{})).To(Succeed())
		Expect(c.poll(context.Background(), &recordingEmitter{err: failure})).To(MatchError(failure))
	})
	DescribeTable("refuses invalid parameters", func(p Params) { Expect(p.Validate()).To(HaveOccurred()) },
		Entry("no connection", Params{}),
		Entry("negative poll", Params{Connection: "example", Poll: "-1s"}),
		Entry("zero poll", Params{Connection: "example", Poll: "0s"}),
		Entry("bad poll", Params{Connection: "example", Poll: "later"}),
		Entry("negative top", Params{Connection: "example", Top: -1}),
		Entry("negative floor", Params{Connection: "example", MinExecutions: -1}),
		Entry("empty exclusion", Params{Connection: "example", Text: []string{"!"}}),
	)
})
