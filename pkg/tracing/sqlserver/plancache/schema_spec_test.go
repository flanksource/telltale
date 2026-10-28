package plancache

import (
	"context"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/query"
	_ "github.com/flanksource/commons-db/query/providers/sqlite"
	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/commons-db/recordstore/recordresults"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/target"
	"github.com/flanksource/telltale/pkg/tracing/tracingtest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
)

type sampleFixture struct {
	handler
	rows []Sample
}

func (sampleFixture) Prepare(ctx dbcontext.Context, _ Params, _ tracing.Emitter[Sample]) (dbcontext.Context, func() error, error) {
	return ctx, func() error { return nil }, nil
}
func (f sampleFixture) Handle(ctx dbcontext.Context, _ Params, out tracing.Emitter[Sample], _ tracing.Records[Sample]) error {
	for _, row := range f.rows {
		if err := out.Emit(ctx, row); err != nil {
			return err
		}
	}
	return nil
}

var _ = Describe("plan cache result schema", func() {
	It("appends repeated handles and executes by_query through the SQLite record profile", func() {
		schema := (handler{}).Schema()
		Expect(schema.KeyColumn).To(BeEmpty())
		Expect(schema.TimeColumn).To(Equal("sampledAt"))
		Expect(schema.Follow).To(BeTrue())
		Expect(schema.Indexes).To(ContainElement(recordstore.IndexDef{Columns: []string{"planHandle", "sampledAt"}}))
		Expect(schema.SearchColumns).To(ConsistOf("sql", "object"))
		Expect(schema.Views).To(ContainElement(HaveField("Name", "by_query")))
		rows := []Sample{
			{SampledAt: planCreated, PlanHandle: "0x01", QueryHash: "0xAA", Executions: 2, CPUMs: 4, ElapsedMs: 10, LogicalReads: 5, AvgElapsedMs: 5, SQL: "SELECT 1"},
			{SampledAt: planCreated, PlanHandle: "0x01", QueryHash: "0xAA", Executions: 3, CPUMs: 6, ElapsedMs: 21, LogicalReads: 8, AvgElapsedMs: 7, SQL: "SELECT 1"},
			{SampledAt: planCreated, PlanHandle: "0x02", QueryHash: "0xAA", Executions: 1, CPUMs: 1, ElapsedMs: 3, LogicalReads: 2, AvgElapsedMs: 3, SQL: "SELECT 2"},
		}
		plugin := tracing.NewHandler[Params, Sample](sampleFixture{rows: rows}, tracing.Capabilities{Follow: true}, target.Connection)
		env := tracingtest.NewEnv(map[string]tracing.TracePlugin{"sql_plancache": plugin})
		info := tracingtest.Ended(env.Start("sql_plancache", `{"connection":"connection://example"}`))
		Expect(info.Error).To(BeEmpty())
		Expect(env.Rows(info)).To(HaveLen(3))
		got := env.Read("sql_plancache/by_query", info.Events.Stream, query.PageRequest{Limit: 20}, nil)
		Expect(got).To(HaveLen(1))
		Expect(got[0]).To(MatchKeys(IgnoreExtras, Keys{
			"queryHash": Equal("0xAA"), "executions": BeNumerically("==", 6), "cpuMs": BeNumerically("==", 11),
			"elapsedMs": BeNumerically("==", 34), "logicalReads": BeNumerically("==", 15),
			"avgElapsedMs": BeNumerically("==", 7), "plans": BeNumerically("==", 2), "sql": Equal("SELECT 1"),
		}))
		_, err := env.Runtime.Summarize(context.Background(), tracing.TraceRef{Kind: "sql_plancache", Stream: info.Events.Stream})
		Expect(err).NotTo(HaveOccurred())
		var _ recordresults.ResultType[Sample] = schema
	})
})
