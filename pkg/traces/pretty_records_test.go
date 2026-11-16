package traces_test

import (
	"encoding/json"
	"slices"
	"time"

	"github.com/flanksource/clicky/api"
	clickyexec "github.com/flanksource/clicky/exec"
	dbgorm "github.com/flanksource/commons-db/gorm"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/commons/har"
	"github.com/flanksource/commons/har/harpretty"
	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/tracing/sqlserver/xetrace"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("styled trace records", func() {
	type example struct {
		kind     string
		row      query.Row
		contains []string
	}
	examples := []example{
		{"http", query.Row{"request": map[string]any{"method": "GET", "url": "https://example.test/orders"}, "response": map[string]any{"status": 200}, "time": 12.0}, []string{"GET", "https://example.test/orders", "200"}},
		{"sql", query.Row{"sql": "SELECT id FROM orders", "durationMs": 1500.0, "rows": 3}, []string{"SLOW SQL", "[1500ms]", "[rows:3]", "SELECT id FROM orders"}},
		{"http_proxy", query.Row{"request": map[string]any{"method": "POST", "url": "https://example.test/orders"}, "response": map[string]any{"status": 201}, "time": 12.0}, []string{"POST", "https://example.test/orders", "201"}},
		{"pg_proxy", query.Row{"sql": "SELECT id FROM orders", "durationMs": 1500.0, "rows": 3}, []string{"SLOW SQL", "[1500ms]", "[rows:3]", "SELECT id FROM orders"}},
		{"sql_xevent", query.Row{"name": "sql_statement_completed", "sql": "SELECT id FROM orders", "durationMs": 12.0, "rowCount": 3}, []string{"SELECT id FROM orders", "rows"}},
		{"sql_plancache", query.Row{"sql": "SELECT id FROM orders", "executions": 4, "avgElapsedMs": 12.0, "database": "checkout"}, []string{"SELECT id FROM orders", "4", "checkout"}},
		{"sql_top", query.Row{"database": "checkout", "sessionId": 41, "state": "active", "waitType": "Lock:transactionid", "elapsedMs": 1500.0, "blocked": true, "blockedBy": 40, "sql": "UPDATE orders SET paid = true"}, []string{"checkout", "#41", "Lock:transactionid", "blocked by #40", "UPDATE orders"}},
		{"sql_deadlock", query.Row{"database": "checkout", "victims": 1, "participants": 2, "victimStatement": "UPDATE orders SET total = 1"}, []string{"deadlock", "checkout", "UPDATE orders"}},
		{"exec", query.Row{"name": "git", "args": []string{"status"}, "exitCode": 0}, []string{"git", "status"}},
		{"entity", query.Row{"entity": "widget", "verb": "update", "targetId": "w-1", "surface": "http", "durationMs": 12.0, "error": "update rejected"}, []string{"widget", "update", "w-1", "http", "update rejected"}},
		{"task", query.Row{"phase": "status", "name": "deploy api", "status": "running", "progress": 2, "maxValue": 5}, []string{"status", "deploy api", "running", "2/5"}},
		{"terminal", query.Row{"kind": "r", "width": 120, "height": 40}, []string{"resize", "120", "40"}},
		{"llm", query.Row{"phase": "response", "model": "example-model", "usage": map[string]any{"inputTokens": 12, "outputTokens": 3}, "cost": 0.004}, []string{"response", "example-model", "12", "3", "$0.004"}},
		{"logs", query.Row{"severity": "error", "logger": "checkout", "message": "payment declined"}, []string{"ERROR", "checkout", "payment declined"}},
		{"spans", query.Row{"service": "checkout", "operation": "orders.submit", "status": "error", "durationMs": 12.0}, []string{"checkout", "orders.submit", "error"}},
		{"goprof", query.Row{"pid": 123, "profile": "heap", "value": 4096, "unit": "bytes", "frames": []map[string]any{{"func": "checkout.allocate"}}}, []string{"123", "heap", "checkout.allocate"}},
		{"stacks", query.Row{"pid": 123, "goroutineId": 7, "state": "IO wait", "frames": []map[string]any{{"func": "checkout.read"}}}, []string{"123", "7", "IO wait", "checkout.read"}},
		{"jvm_jfr", query.Row{"event": "alloc", "thread": "worker", "frame": "Checkout.allocate", "value": 4096, "unit": "bytes"}, []string{"alloc", "worker", "Checkout.allocate"}},
		{"http_top", query.Row{"direction": "outgoing", "method": "GET", "url": "https://example.test/orders/7", "elapsedMs": 125.0}, []string{"outgoing", "GET", "https://example.test/orders/7", "125"}},
		{"jms", query.Row{"operation": "send", "direction": "outgoing", "outcome": "success", "destination": "orders"}, []string{"send", "outgoing", "success", "orders"}},
		{"jvm_notifications", query.Row{"type": "java.management.memory.threshold.exceeded", "sequenceNumber": 12, "message": "heap threshold exceeded", "source": "java.lang:type=Memory"}, []string{"java.management.memory.threshold.exceeded", "heap threshold exceeded", "java.lang:type=Memory", "12"}},
		{"jvm_ognl", query.Row{"expression": "@java.lang.Runtime@getRuntime().freeMemory()", "result": 4096}, []string{"@java.lang.Runtime@getRuntime().freeMemory()"}},
		{"jvm_redefine", query.Row{"class": "example.Checkout", "loader": "app-loader", "applied": true, "digest": "patched-digest"}, []string{"example.Checkout", "app-loader", "patched-digest"}},
		{"jvm_trace", query.Row{"kind": "return", "class": "example.Checkout", "method": "submit", "descriptor": "(J)Z", "value": true}, []string{"return", "example.Checkout", "submit", "(J)Z"}},
		{"sql_top_active", query.Row{"operationId": "sql/7", "api": "jdbc", "elapsedMs": 125.0, "sql": "SELECT id FROM orders"}, []string{"sql/7", "jdbc", "125", "SELECT id FROM orders"}},
		{"prometheus", query.Row{"endpoint": "http://localhost/metrics", "health": "up", "samples": 42, "durationMs": 12.0}, []string{"http://localhost/metrics", "up", "42"}},
		{"query", query.Row{"profile": "request-rate", "provider": "prometheus", "row": map[string]any{"value": 7}}, []string{"request-rate", "prometheus", "7"}},
		{"process", query.Row{"pid": 123, "name": "checkout", "command": "checkout serve"}, []string{"123", "checkout", "serve"}},
		{"process_metrics", query.Row{"entity": "123@started", "cpuPercent": 12.5, "rssBytes": 4096}, []string{"123@started", "12.5", "CPU"}},
		{"process_facets", query.Row{"process": "123@started", "facet": "files", "operation": "open", "entity": "/orders.db"}, []string{"123@started", "files", "open", "/orders.db"}},
	}

	for _, kind := range []string{"goprof", "http_top", "jvm_jfr", "process", "prometheus", "query", "sql_plancache", "sql_top", "stacks"} {
		examples = append(examples, example{kind: kind + "_frames", row: query.Row{"source": "worker", "completedAt": "2026-11-11T10:00:00Z", "retained": 1, "observed": 2}, contains: []string{"worker", "1/2 rows"}})
	}
	DescribeTableSubtree("presents native fields with a typed formatter", func(e example) {
		It("keeps meaningful content and semantic styles", func() {
			kinds, err := traces.Kinds()
			Expect(err).NotTo(HaveOccurred())
			before, err := json.Marshal(e.row)
			Expect(err).NotTo(HaveOccurred())
			p, err := kinds.Present(e.kind, e.row)
			Expect(err).NotTo(HaveOccurred())
			for _, content := range e.contains {
				Expect(p.Summary.String()).To(ContainSubstring(content))
			}
			Expect(p.Summary.HTML()).To(ContainSubstring("color:"))
			after, err := json.Marshal(e.row)
			Expect(err).NotTo(HaveOccurred())
			Expect(after).To(Equal(before))
		})
	}, func() []TableEntry {
		entries := []TableEntry{}
		for _, e := range examples {
			entries = append(entries, Entry(e.kind, e))
		}
		return entries
	}())

	It("covers every registered built-in record and named result", func() {
		kinds, err := traces.Kinds()
		Expect(err).NotTo(HaveOccurred())
		infos, err := kinds.List()
		Expect(err).NotTo(HaveOccurred())
		var actual, expected []string
		for _, e := range examples {
			actual = append(actual, e.kind)
		}
		for _, info := range infos {
			if slices.Contains(registrationNames(), info.Name) {
				expected = append(expected, info.Name)
				plugin, ok := kinds.Get(info.Name)
				Expect(ok).To(BeTrue())
				for _, table := range plugin.ResultTables() {
					expected = append(expected, info.Name+"_"+table.Name)
				}
			}
		}
		Expect(actual).To(ConsistOf(expected))
	})

	DescribeTable("reuses established formatting without changing its styles", func(kind string, row query.Row, expected api.Text) {
		kinds, err := traces.Kinds()
		Expect(err).NotTo(HaveOccurred())
		p, err := kinds.Present(kind, row)
		Expect(err).NotTo(HaveOccurred())
		Expect(p.Summary).To(Equal(expected))
	},
		Entry("HTTP HAR", "http", query.Row{"request": map[string]any{"method": "GET", "url": "https://example.test"}, "response": map[string]any{"status": 200}, "time": 12.0}, harpretty.Entry(har.Entry{Request: har.Request{Method: "GET", URL: "https://example.test"}, Response: har.Response{Status: 200}, Time: 12}).Pretty()),
		Entry("SQL slow log", "sql", query.Row{"sql": "SELECT 1", "durationMs": 1500.0, "rows": 3}, dbgorm.SQLTrace{SQL: "SELECT 1", Duration: 1500 * time.Millisecond, Rows: 3, Slow: true}.Pretty()),
		Entry("execution", "exec", query.Row{"name": "git", "args": []string{"status"}, "exitCode": 0}, clickyexec.ExecResult{Command: "git", Args: []string{"status"}, ExitCode: 0}.Pretty()),
		Entry("SQL extended event", "sql_xevent", query.Row{"name": "sql_statement_completed", "sql": "SELECT 1", "durationMs": 12.0}, xetrace.StreamLine(xetrace.Event{Name: "sql_statement_completed", SQL: "SELECT 1", Duration: 12 * time.Millisecond}, xetrace.StreamLineOptions{ShowDatabase: true})),
	)
})
