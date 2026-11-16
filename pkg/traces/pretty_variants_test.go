package traces_test

import (
	"strings"

	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/telltale/pkg/traces"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("styled trace variants", func() {
	DescribeTable("preserves status and failure semantics", func(kind string, row query.Row, content, color string) {
		kinds, err := traces.Kinds()
		Expect(err).NotTo(HaveOccurred())
		p, err := kinds.Present(kind, row)
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.Join(strings.Fields(p.Summary.String()), " ")).To(ContainSubstring(content))
		Expect(strings.ToLower(p.Summary.HTML())).To(ContainSubstring(color))
	},
		Entry("HTTP success", "http", query.Row{"request": map[string]any{"method": "GET"}, "response": map[string]any{"status": 200}}, "200", "#22c55e"),
		Entry("HTTP redirect", "http", query.Row{"request": map[string]any{"method": "GET"}, "response": map[string]any{"status": 302}}, "302", "#3b82f6"),
		Entry("HTTP client error", "http", query.Row{"request": map[string]any{"method": "GET"}, "response": map[string]any{"status": 404}}, "404", "#eab308"),
		Entry("HTTP server error", "http", query.Row{"request": map[string]any{"method": "GET"}, "response": map[string]any{"status": 503}}, "503", "#ef4444"),
		Entry("HTTP transport failure", "http", query.Row{"error": "connection refused"}, "connection refused", "#ef4444"),
		Entry("SQL normal", "sql", query.Row{"sql": "SELECT 1", "rows": -1, "durationMs": 2.0}, "[rows:-1]", "#3b82f6"),
		Entry("SQL error", "sql", query.Row{"sql": "SELECT missing", "error": "missing table"}, "missing table", "#ef4444"),
		Entry("exec error", "exec", query.Row{"name": "git", "exitCode": 1, "error": "permission denied"}, "permission denied", "#ef4444"),
		Entry("terminal output", "terminal", query.Row{"kind": "o", "data": "git version\n"}, "git version", "#3b82f6"),
		Entry("terminal snapshot", "terminal", query.Row{"kind": "snapshot", "data": "ready"}, "snapshot", "#3b82f6"),
		Entry("terminal failed exit", "terminal", query.Row{"kind": "exit", "exitCode": 1}, "exit 1", "#ef4444"),
		Entry("LLM request", "llm", query.Row{"phase": "request", "model": "example-model", "prompt": "private prompt"}, "request", "#3b82f6"),
	)

	DescribeTable("keeps expanded payloads out of the compact row", func(kind string, row query.Row, detail string) {
		kinds, err := traces.Kinds()
		Expect(err).NotTo(HaveOccurred())
		p, err := kinds.Present(kind, row)
		Expect(err).NotTo(HaveOccurred())
		Expect(p.Summary.String()).NotTo(ContainSubstring(detail))
		Expect(p.Detail.String()).To(ContainSubstring(detail))
	},
		Entry("HTTP response body", "http", query.Row{"request": map[string]any{"method": "GET"}, "response": map[string]any{"status": 200, "content": map[string]any{"mimeType": "application/json", "text": map[string]any{"result": "recorded-response"}}}}, "recorded-response"),
		Entry("execution stdout", "exec", query.Row{"name": "git", "exitCode": 0, "stdoutTail": "recorded-stdout"}, "recorded-stdout"),
		Entry("LLM prompt", "llm", query.Row{"phase": "request", "model": "example-model", "prompt": "recorded-prompt"}, "recorded-prompt"),
	)
})
