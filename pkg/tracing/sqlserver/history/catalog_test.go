package history_test

import (
	deadlocks "github.com/flanksource/telltale/pkg/tracing/sqlserver/history"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("FilePattern", func() {
	DescribeTable("matches every rollover file of the session in the server's own directory",
		func(current, pattern string) {
			Expect(deadlocks.FilePattern(current)).To(Equal(pattern))
		},
		Entry("Linux", "/var/opt/mssql/log/system_health_0_134019856532450000.xel", "/var/opt/mssql/log/system_health*.xel"),
		Entry("Windows", `D:\rdsdbdata\Log\system_health_0_134019856532450000.xel`, `D:\rdsdbdata\Log\system_health*.xel`),
	)

	It("refuses a file name without the rollover suffix", func() {
		_, err := deadlocks.FilePattern("/var/opt/mssql/log/system_health.xel")
		Expect(err).To(MatchError(ContainSubstring("system_health.xel")))
	})
})

var _ = Describe("ScopeDatabases", func() {
	DescribeTable("pushes only literal names into SQL",
		func(values []string, want deadlocks.DatabaseScope) {
			Expect(deadlocks.ScopeDatabases(values)).To(Equal(want))
		},
		Entry("no filter: the connected database", nil, deadlocks.DatabaseScope{}),
		Entry("literal names, comma-separated or repeated",
			[]string{"tenant_a, tenant_b", "tenant_c"},
			deadlocks.DatabaseScope{Names: []string{"tenant_a", "tenant_b", "tenant_c"}}),
		Entry("the whole instance", []string{"*"}, deadlocks.DatabaseScope{All: true}),
		Entry("a wildcard", []string{"tenant_*"}, deadlocks.DatabaseScope{All: true}),
		Entry("an exclusion", []string{"!tempdb"}, deadlocks.DatabaseScope{All: true}),
	)
})
