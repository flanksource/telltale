package main

import (
	"github.com/flanksource/commons-db/query"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("playground sample selection", func() {
	DescribeTable("keeps meaningful native event variants",
		func(kind string, row query.Row, key string) { Expect(exportSampleKey(kind, row)).To(Equal(key)) },
		Entry("JFR CPU stack", "jvm_jfr", query.Row{"event": "cpu", "stack": "DemoWorkload.compute", "value": 2}, "cpu"),
		Entry("JFR runtime-only sample", "jvm_jfr", query.Row{"event": "cpu", "stack": "[no stack]", "value": 2}, ""),
		Entry("LLM request", "llm", query.Row{"phase": "request"}, "request"),
		Entry("PTY resize", "terminal", query.Row{"kind": "r"}, "r"),
		Entry("deadlock error", "sql_xevent", query.Row{"name": "error_reported", "errorNumber": 1205}, "error_reported"),
		Entry("database context notice", "sql_xevent", query.Row{"name": "error_reported", "errorNumber": 5701}, ""),
		Entry("process metrics", "process_metrics", query.Row{"rssBytes": 4096, "cpuPercent": 1.5}, "record"),
		Entry("process descriptor", "process_facets", query.Row{"facet": "files", "operation": "open", "resource": map[string]any{"name": "playground-observed-recording"}}, "files/open"),
		Entry("existing process descriptor", "process_facets", query.Row{"facet": "files", "operation": "open", "resource": map[string]any{"name": "/work/orders.db"}}, "files/open"),
	)
})
