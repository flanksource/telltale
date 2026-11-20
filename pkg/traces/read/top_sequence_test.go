package read_test

import (
	"context"
	"time"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/commons-db/recordstore/sampleframe"
	"github.com/flanksource/telltale/pkg/traces/read"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/target"
	"github.com/flanksource/telltale/pkg/tracing/tracingtest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type sequencedTopFixture struct{ topFixture }

func (sequencedTopFixture) Handle(ctx dbcontext.Context, _ struct{}, out tracing.Emitter[topRow], _ tracing.Records[topRow]) error {
	at := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	for _, snapshot := range []struct {
		source   string
		tick     int64
		rows     []topRow
		complete bool
	}{
		{"first", 2, nil, true},
		{"first", 1, []topRow{{ID: "obsolete-first", CPU: 10}}, true},
		{"second", 7, []topRow{{ID: "fresh-second", CPU: 20}}, true},
		{"second", 4, []topRow{{ID: "obsolete-second", CPU: 30}}, true},
		{"second", 9, nil, false},
	} {
		frame := sampleframe.Frame{Source: snapshot.source, Tick: snapshot.tick, StartedAt: at, CompletedAt: at, Observed: len(snapshot.rows), Summary: map[string]any{"complete": snapshot.complete}}
		if err := tracing.PublishFrame(ctx, out, frame, snapshot.rows); err != nil {
			return err
		}
	}
	return nil
}

var _ = Describe("producer-sequenced Top reads", func() {
	It("uses the newest complete producer snapshot per source despite reordered delivery", func() {
		plugin := tracing.NewHandler[struct{}, topRow](sequencedTopFixture{}, tracing.Capabilities{Live: true}, target.Self).WithTop(tracing.TopSpec{PerSource: true, Identity: []string{"id"}, Order: query.Order{{Column: "cpu", Desc: true}}})
		env := tracingtest.NewEnv(map[string]tracing.TracePlugin{"sampled": plugin})
		info := tracingtest.Ended(env.Start("sampled", `{}`))
		Expect(info.Error).To(BeEmpty())
		ref := read.Ref{Runtime: env.Runtime, TraceRef: tracing.TraceRef{Kind: "sampled", Stream: info.Events.Stream}}
		result, err := read.Top(context.Background(), ref, read.TopQuery{})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Rows).To(ConsistOf(HaveKeyWithValue("id", "fresh-second")))
		Expect(result.Frames).To(ContainElements(And(HaveField("Source", "first"), HaveField("Tick", int64(2))), And(HaveField("Source", "second"), HaveField("Tick", int64(7)))))
		first, err := read.Top(context.Background(), ref, read.TopQuery{Source: "first"})
		Expect(err).NotTo(HaveOccurred())
		Expect(first.Rows).To(BeEmpty())
		Expect(first.Frame.Tick).To(Equal(int64(2)))
		frames, err := read.Frames(context.Background(), ref)
		Expect(err).NotTo(HaveOccurred())
		Expect(frames).To(HaveLen(5))
		history, err := read.Top(context.Background(), ref, read.TopQuery{Frame: frames[1].ID})
		Expect(err).NotTo(HaveOccurred())
		Expect(history.Rows).To(ConsistOf(HaveKeyWithValue("id", "obsolete-first")))
	})
})
