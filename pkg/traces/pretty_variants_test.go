package traces_test

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/flanksource/clicky"
	"github.com/flanksource/clicky/api"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/telltale/pkg/traces"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("styled trace variants", func() {
	DescribeTable("shows HTTP direction in compact events and active samples", func(direction, arrow string) {
		kinds, err := traces.Kinds()
		Expect(err).NotTo(HaveOccurred())
		const endpoint = "https://example.test/projects"
		exchange, err := kinds.Present("http", query.Row{
			"direction": direction, "request": map[string]any{"method": "GET", "url": endpoint},
			"response": map[string]any{"status": 200},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(exchange.Summary.String()).To(ContainSubstring(arrow + " GET " + endpoint))
		Expect(exchange.Detail.String()).To(ContainSubstring(arrow + " GET " + endpoint))

		sample, err := kinds.Present("http_top", query.Row{
			"direction": direction, "method": "GET", "url": endpoint, "elapsedMs": 84.0,
		})
		Expect(err).NotTo(HaveOccurred())
		for _, summary := range []api.Text{exchange.Summary, sample.Summary} {
			presentation, err := clicky.Format(summary, clicky.FormatOptions{Format: "ansi"})
			Expect(err).NotTo(HaveOccurred())
			Expect(strings.Join(strings.Fields(ansi.Strip(presentation)), " ")).To(ContainSubstring(arrow + " GET " + endpoint))
		}
	}, Entry("incoming", "incoming", "←"), Entry("outgoing", "outgoing", "→"))

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
		Entry("OGNL result", "jvm_ognl", query.Row{"expression": "@java.lang.Runtime@getRuntime().freeMemory()", "result": 4096}, "4096"),
		Entry("JVM method value", "jvm_trace", query.Row{"kind": "return", "class": "example.Checkout", "method": "findOrder", "descriptor": "(J)Ljava/util/Map;", "value": map[string]any{"orderId": "recorded-method-value"}}, "recorded-method-value"),
	)
})
