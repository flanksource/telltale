package read_test

import (
	"context"
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

type capturedProfileRow struct {
	Values map[string]any `json:"row"`
}
type capturedProfileFixture struct{}

func (capturedProfileFixture) Params() struct{} { return struct{}{} }
func (capturedProfileFixture) Schema() recordresults.ResultType[capturedProfileRow] {
	return recordresults.ResultType[capturedProfileRow]{Title: "Captured profile"}
}
func (capturedProfileFixture) Handle(ctx dbcontext.Context, _ struct{}, out tracing.Emitter[capturedProfileRow], _ tracing.Records[capturedProfileRow]) error {
	at := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	return tracing.PublishFrame(ctx, out, sampleframe.Frame{Source: "saved-profile", StartedAt: at, CompletedAt: at, Observed: 3,
		Summary: map[string]any{"columns": []query.ColumnDef{{Name: "job", Type: query.ColumnTypeString}, {Name: "elapsed", Type: query.ColumnTypeDuration, Unit: "ms"}, {Name: "enabled", Type: query.ColumnTypeBoolean}}, "order": query.Order{{Column: "elapsed", Desc: true}}}},
		[]capturedProfileRow{{Values: map[string]any{"job": "fast", "elapsed": 0, "enabled": false}}, {Values: map[string]any{"job": "slow", "elapsed": 1200, "enabled": true}}, {Values: map[string]any{"job": "medium", "elapsed": 500, "enabled": false}}})
}

var _ = Describe("Top captured profile columns", func() {
	It("uses original duration and boolean filters before profile ordering and pagination", func() {
		env := tracingtest.NewEnv(map[string]tracing.TracePlugin{"sampled": tracing.NewHandler[struct{}, capturedProfileRow](capturedProfileFixture{}, tracing.Capabilities{Live: true}, target.Self).WithTop(tracing.TopSpec{ProjectionColumn: "row"})})
		info := tracingtest.Ended(env.Start("sampled", `{}`))
		Expect(info.Error).To(BeEmpty())
		ref := read.Ref{Runtime: env.Runtime, TraceRef: tracing.TraceRef{Kind: "sampled", Stream: info.Events.Stream}}
		result, err := read.Top(context.Background(), ref, read.TopQuery{Query: read.Query{Filters: []string{"elapsed=<1s", "enabled=false"}, Limit: 1}})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Count).To(Equal(2))
		Expect(result.Rows[0]["job"]).To(Equal("medium"))
		Expect(result.Rows[0]["enabled"]).To(Equal(false))
		Expect(result.Display.Columns[0].Name).To(Equal("job"))
		Expect(result.Display.RowDetail).To(HaveLen(1))
		Expect(result.Display.RowDetail[0]).NotTo(BeNil())
	})
})
