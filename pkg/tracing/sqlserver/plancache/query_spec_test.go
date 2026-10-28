package plancache

import (
	"strings"

	"github.com/flanksource/clicky"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/sqlserver"
	"gorm.io/gorm"
)

func dryDB() *gorm.DB {
	GinkgoHelper()
	db, err := gorm.Open(sqlserver.Open(""), &gorm.Config{DryRun: true, DisableAutomaticPing: true})
	Expect(err).NotTo(HaveOccurred())
	return db
}

func renderQuery(f Filter) string {
	GinkgoHelper()
	db := dryDB()
	q, err := baseQuery(db, f)
	Expect(err).NotTo(HaveOccurred())
	q, err = Sort.Apply(q.Select(selectList), f.SortOptions)
	Expect(err).NotTo(HaveOccurred())
	limit, offset, err := resolvePaging(f)
	Expect(err).NotTo(HaveOccurred())
	var rows []cachedPlanRow
	tx := q.Limit(limit).Offset(offset).Find(&rows)
	return db.Explain(tx.Statement.SQL.String(), tx.Statement.Vars...)
}

var _ = Describe("plan cache SQL", func() {
	It("excludes the sampler's marked SQL in the DMV WHERE clause", func() {
		Expect(renderQuery(Filter{})).To(ContainSubstring("CHARINDEX('/* telltale:sql_plancache */', st.text) = 0"))
	})
	It("uses a plan-grain LEFT JOIN and the non-reserved cp alias", func() {
		sql := renderQuery(Filter{})
		for _, fragment := range []string{"sys.dm_exec_cached_plans AS cp", "CROSS APPLY sys.dm_exec_sql_text(cp.plan_handle) AS st", "LEFT JOIN (", "FROM sys.dm_exec_query_stats", "GROUP BY plan_handle", ") AS qs ON qs.plan_handle = cp.plan_handle", "SUM(total_logical_writes)", "MIN(creation_time)", "MAX(last_execution_time)", "MIN(query_hash)", "MIN(query_plan_hash)"} {
			Expect(sql).To(ContainSubstring(fragment))
		}
		Expect(sql).NotTo(ContainSubstring("SUM((SELECT"))
		Expect(sql).NotTo(ContainSubstring(" AS plan "))
		Expect(sql).To(ContainSubstring("cp.cacheobjtype = 'Compiled Plan'"))
		Expect(sql).To(ContainSubstring("st.dbid = DB_ID()"))
		Expect(sql).To(ContainSubstring("FETCH NEXT 100 ROWS ONLY"))
		Expect(sql).To(ContainSubstring("ORDER BY " + avgCPUMicros + " DESC,cp.plan_handle ASC"))
	})
	It("preserves never-run plans when stubs are included", func() {
		sql := renderQuery(Filter{IncludeStubs: true})
		Expect(sql).NotTo(ContainSubstring("cp.cacheobjtype ="))
		Expect(sql).NotTo(ContainSubstring("INNER JOIN"))
	})
	DescribeTable("pushes filters into WHERE", func(f Filter, fragments []string) {
		sql := renderQuery(f)
		for _, fragment := range fragments {
			Expect(sql).To(ContainSubstring(fragment))
		}
	},
		Entry("database patterns", Filter{Database: clicky.MultiFilter{"warehouse*", "!tempdb"}}, []string{"LOWER(DB_NAME(st.dbid)) LIKE 'warehouse%'", "<> 'tempdb'"}),
		Entry("text patterns", Filter{Text: clicky.MultiFilter{"ledger", "!secret*"}}, []string{"LOWER(st.text) LIKE '%ledger%'", "LOWER(st.text) NOT LIKE 'secret%'"}),
		Entry("literal SQL metacharacters", Filter{Text: clicky.MultiFilter{"rate_10%[x]"}}, []string{`%rate\_10\%\[x]%`}),
		Entry("ranges", Filter{Executions: ">5", AvgCPU: ">=500ms", SizeKB: ">64"}, []string{"qs.execution_count > 5", avgCPUMicros + " >= 500000", "cp.size_in_bytes > 65536"}),
		Entry("unused lower bound", Filter{UnusedFor: ">24h"}, []string{"qs.last_execution_time IS NULL OR", "> 86400000"}),
		Entry("object and type", Filter{Object: clicky.MultiFilter{"report"}, ObjType: clicky.MultiFilter{"Proc"}}, []string{"LOWER(OBJECT_NAME(st.objectid, st.dbid)) = 'report'", "LOWER(cp.objtype) = 'proc'"}),
	)
	It("supports all databases and exclude-all without adding the connected database restriction", func() {
		Expect(renderQuery(Filter{Database: clicky.MultiFilter{"*"}})).NotTo(ContainSubstring("st.dbid = DB_ID()"))
		Expect(renderQuery(Filter{Database: clicky.MultiFilter{"!*"}})).To(ContainSubstring("1 = 0"))
	})
	It("validates every sort key and binds handles", func() {
		for key, expr := range Sort.Columns {
			Expect(renderQuery(Filter{SortOptions: clicky.SortOptions{Key: key}})).To(ContainSubstring("ORDER BY " + expr + " ASC"))
		}
		_, err := Sort.Apply(dryDB(), clicky.SortOptions{Key: "; DROP TABLE example"})
		Expect(err).To(HaveOccurred())
		_, err = Sort.Apply(dryDB(), clicky.SortOptions{Key: "size", Direction: "invalid"})
		Expect(err).To(HaveOccurred())
		q, err := baseQuery(dryDB(), Filter{PlanHandles: []string{"0x01", "0x02"}, QueryHash: "0xAA"})
		Expect(err).NotTo(HaveOccurred())
		var rows []cachedPlanRow
		tx := q.Select(selectList).Find(&rows)
		Expect(tx.Statement.Vars).To(ContainElements([]byte{1}, []byte{2}, []byte{0xAA}))
	})
	It("rejects malformed filters and paging", func() {
		for _, f := range []Filter{{PlanHandles: []string{"invalid"}}, {QueryHash: "0xZZ"}, {Executions: "NaN"}, {Executions: ">5,<5"}, {Database: clicky.MultiFilter{"!"}}} {
			_, err := baseQuery(dryDB(), f)
			Expect(err).To(HaveOccurred())
		}
		for _, f := range []Filter{{Limit: -1}, {Limit: MaxLimit + 1}, {Offset: -1}} {
			_, _, err := resolvePaging(f)
			Expect(err).To(HaveOccurred())
		}
		Expect(strings.Contains(renderQuery(Filter{UnusedFor: "<24h"}), "qs.last_execution_time IS NULL OR")).To(BeFalse())
	})
})
