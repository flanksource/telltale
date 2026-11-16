package sqltop

import (
	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/query"
	_ "github.com/flanksource/commons-db/query/providers/sqlite"
	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/target"
	"github.com/flanksource/telltale/pkg/tracing/tracingtest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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

// filterKinds are the filter every column of a sample offers.
var filterKinds = map[string]query.ColumnFilterKind{
	"sampledAt": "time", "engine": "terms", "database": "terms", "sessionId": "exact", "requestId": "exact",
	"blockedBy": "exact", "blocked": "boolean", "user": "terms", "application": "terms", "clientHost": "terms",
	"state": "terms", "command": "terms", "waitType": "terms", "waitResource": "exact", "waitMs": "duration",
	"startedAt": "time", "transactionStartedAt": "time", "elapsedMs": "duration", "transactionMs": "duration",
	"cpuMs": "duration", "logicalReads": "range", "reads": "range", "writes": "range", "memoryBytes": "range",
	"openTransactions": "range", "queryId": "exact", "sql": "text",
}

var _ = Describe("sql top result schema", func() {
	It("offers a filter on every column", func() {
		catalog := tracing.NewKinds()
		Expect(catalog.RegisterKind("sql_top", Kind())).To(Succeed())
		infos, err := catalog.List()
		Expect(err).NotTo(HaveOccurred())
		Expect(infos).To(HaveLen(1))
		got := map[string]query.ColumnFilterKind{}
		for _, column := range infos[0].Columns {
			Expect(column.Filter).NotTo(BeNil(), "column %s offers no filter", column.Name)
			got[column.Name] = column.Filter.Kind
		}
		Expect(got).To(Equal(filterKinds))
	})
	It("appends a session's repeated samples and filters them by blocked and elapsed", func() {
		schema := (handler{}).Schema()
		Expect(schema.TimeColumn).To(Equal("sampledAt"))
		Expect(schema.Follow).To(BeTrue())
		Expect(schema.Indexes).To(ContainElement(recordstore.IndexDef{Columns: []string{"sessionId", "sampledAt"}}))
		Expect(schema.SearchColumns).To(ConsistOf("sql", "user", "application"))
		rows := []Sample{
			{SampledAt: serverTime, Engine: EnginePostgres, SessionID: 7, ElapsedMs: 900, BlockedBy: 9, Blocked: true, SQL: "UPDATE orders"},
			{SampledAt: serverTime.Add(5e9), Engine: EnginePostgres, SessionID: 7, ElapsedMs: 5900, BlockedBy: 9, Blocked: true, SQL: "UPDATE orders"},
			{SampledAt: serverTime, Engine: EnginePostgres, SessionID: 9, ElapsedMs: 50, SQL: "SELECT 1"},
		}
		plugin := tracing.NewHandler[Params, Sample](sampleFixture{rows: rows}, tracing.Capabilities{Follow: true}, target.Connection)
		env := tracingtest.NewEnv(map[string]tracing.TracePlugin{"sql_top": plugin})
		info := tracingtest.Ended(env.Start("sql_top", `{"connection":"connection://example"}`))
		Expect(info.Error).To(BeEmpty())
		Expect(env.Rows(info)).To(HaveLen(3))
		blocked := env.Read("sql_top", info.Events.Stream, query.PageRequest{Limit: 20}, map[string]any{"filter.blocked": "true", "filter.elapsedMs": ">1s"})
		Expect(blocked).To(HaveLen(1))
		Expect(blocked[0]).To(HaveKeyWithValue("elapsedMs", BeNumerically("==", 5900)))
	})
})
