package read_test

import (
	"context"
	"fmt"
	"strings"
	"time"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/commons-db/recordstore/recordresults"
	"github.com/flanksource/commons-db/recordstore/sampleframe"
	"github.com/flanksource/telltale/pkg/traces/read"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/target"
	"github.com/flanksource/telltale/pkg/tracing/tracingtest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type topRow struct {
	ID  string  `json:"id" filter:"terms" sort:"id"`
	CPU float64 `json:"cpu" filter:"range" sort:"cpu"`
}

type topFixture struct{ empty bool }

type multiTopFixture struct{ topFixture }

func (multiTopFixture) Handle(ctx dbcontext.Context, _ struct{}, out tracing.Emitter[topRow], _ tracing.Records[topRow]) error {
	at := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	for i, source := range []string{"first", "second", "first", "third"} {
		rows := []topRow{{ID: source, CPU: float64(i + 1)}}
		if i == 2 {
			rows = nil
		}
		if err := tracing.PublishFrame(ctx, out, sampleframe.Frame{Source: source, StartedAt: at, CompletedAt: at, Observed: len(rows)}, rows); err != nil {
			return err
		}
	}
	return nil
}

func (topFixture) Params() struct{} { return struct{}{} }
func (topFixture) Schema() recordresults.ResultType[topRow] {
	return recordresults.ResultType[topRow]{Title: "Sample fixture"}
}
func (f topFixture) Handle(ctx dbcontext.Context, _ struct{}, out tracing.Emitter[topRow], _ tracing.Records[topRow]) error {
	at := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	for sample := range 2 {
		rows := []topRow{}
		if sample == 0 || !f.empty {
			for i := range 150 {
				rows = append(rows, topRow{ID: fmt.Sprintf("worker-%03d", i), CPU: float64(i + sample*150)})
			}
		}
		if err := tracing.PublishFrame(ctx, out, sampleframe.Frame{Source: "test", StartedAt: at, CompletedAt: at, Observed: len(rows)}, rows); err != nil {
			return err
		}
	}
	return tracing.PublishFrame(ctx, out, sampleframe.Frame{Source: "test", StartedAt: at, CompletedAt: at, Observed: 1, Error: "capture interrupted"}, []topRow{{ID: "failed", CPU: 1000}})
}

func topRef(empty bool) read.Ref {
	env := tracingtest.NewEnv(map[string]tracing.TracePlugin{"sampled": tracing.NewHandler[struct{}, topRow](topFixture{empty: empty}, tracing.Capabilities{Live: true}, target.Self).WithTop(tracing.TopSpec{Identity: []string{"id"}, Order: query.Order{{Column: "cpu", Desc: true}}})})
	info := tracingtest.Ended(env.Start("sampled", `{}`))
	Expect(info.Error).To(BeEmpty())
	return read.Ref{Runtime: env.Runtime, TraceRef: tracing.TraceRef{Kind: "sampled", Stream: info.Events.Stream}}
}

var _ = Describe("Top reads", func() {
	It("freezes all endpoint samples and respects the latest empty scrape for each source", func() {
		plugin := tracing.NewHandler[struct{}, topRow](multiTopFixture{}, tracing.Capabilities{Live: true}, target.Self).WithTop(tracing.TopSpec{PerSource: true, Identity: []string{"id"}, Order: query.Order{{Column: "cpu", Desc: true}}})
		env := tracingtest.NewEnv(map[string]tracing.TracePlugin{"sampled": plugin})
		info := tracingtest.Ended(env.Start("sampled", `{}`))
		Expect(info.Error).To(BeEmpty())
		ref := read.Ref{Runtime: env.Runtime, TraceRef: tracing.TraceRef{Kind: "sampled", Stream: info.Events.Stream}}
		frames, err := read.Frames(context.Background(), ref)
		Expect(err).NotTo(HaveOccurred())
		latest, err := read.Top(context.Background(), ref, read.TopQuery{})
		Expect(err).NotTo(HaveOccurred())
		Expect(latest.Rows).To(HaveLen(2))
		Expect(latest.Rows).NotTo(ContainElement(HaveKeyWithValue("id", "first")))
		frozen, err := read.Top(context.Background(), ref, read.TopQuery{Frame: strings.Join([]string{frames[0].ID, frames[1].ID}, ",")})
		Expect(err).NotTo(HaveOccurred())
		Expect(frozen.Rows).To(ConsistOf(HaveKeyWithValue("id", "first"), HaveKeyWithValue("id", "second")))
	})
	It("pins pagination to one sample and rejects a cursor reused with different filters", func() {
		ref := topRef(false)
		first, err := read.Top(context.Background(), ref, read.TopQuery{Query: read.Query{Limit: 100}})
		Expect(err).NotTo(HaveOccurred())
		second, err := read.Top(context.Background(), ref, read.TopQuery{Frame: first.Frame.ID, Query: read.Query{Limit: 100, Cursor: first.NextCursor}})
		Expect(err).NotTo(HaveOccurred())
		Expect(second.Rows).To(HaveLen(50))
		Expect(second.Rows[0]["cpu"]).To(BeNumerically("==", 199))
		_, err = read.Top(context.Background(), ref, read.TopQuery{Frame: first.Frame.ID, Query: read.Query{Cursor: first.NextCursor, Filters: []string{"cpu=>200"}}})
		Expect(err).To(HaveOccurred())
	})
	It("rejects unknown filters and ordering even for a successful empty sample", func() {
		ref := topRef(true)
		_, err := read.Top(context.Background(), ref, read.TopQuery{Query: read.Query{Filters: []string{"unknown=1"}}})
		Expect(err).To(HaveOccurred())
		_, err = read.Top(context.Background(), ref, read.TopQuery{Query: read.Query{Sort: "unknown"}})
		Expect(err).To(HaveOccurred())
	})
	It("selects the latest complete sample before filtering, ordering and pagination", func() {
		ref := topRef(false)
		result, err := read.Top(context.Background(), ref, read.TopQuery{Query: read.Query{Limit: 2}})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Rows).To(HaveLen(2))
		Expect(result.Rows[0]["cpu"]).To(BeNumerically("==", 299))
		Expect(result.Count).To(Equal(150))
		result, err = read.Top(context.Background(), ref, read.TopQuery{Query: read.Query{Filters: []string{"cpu=<100"}}})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Rows).To(BeEmpty())
		Expect(result.Frame.Retained).To(Equal(150))
	})
	It("returns a successful empty latest sample instead of resurrecting old rows", func() {
		result, err := read.Top(context.Background(), topRef(true), read.TopQuery{})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Rows).To(BeEmpty())
		Expect(result.Frame.Retained).To(BeZero())
	})
})
