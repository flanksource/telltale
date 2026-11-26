package http_test

import (
	"encoding/json"
	"strings"

	"github.com/flanksource/telltale/pkg/tracing/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("HTTP agent records", func() {
	It("preserves JVM direction, request identity and trace correlation", func() {
		record, err := http.DecodeRecord(json.RawMessage(`{"startedDateTime":"2026-01-02T03:04:05Z","time":12,"request":{"method":"GET","url":"https://example.com/orders/7"},"response":{"status":200},"direction":"incoming","requestId":"request-7","route":"/orders/{id}","handler":"example.OrderController#get","threadId":19,"thread":"request-worker","traceId":"trace-7","spanId":"span-7"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(record.Metadata).To(Equal(http.Metadata{Direction: http.Incoming, RequestID: "request-7", Route: "/orders/{id}", Handler: "example.OrderController#get", ThreadID: 19, Thread: "request-worker", TraceID: "trace-7", SpanID: "span-7"}))
		Expect(http.ToEntry(record).Metadata).To(Equal(record.Metadata))
		Expect(record.Method).To(Equal("GET"))
		Expect(record.Host).To(Equal("example.com"))
		Expect(record.Status).To(Equal(200))
		Expect(record.DurationMs).To(Equal(float64(12)))
	})

	It("does not deduplicate incoming and outgoing copies of the same exchange", func() {
		incoming := http.Exchange{Metadata: http.Metadata{Direction: http.Incoming}, Method: "GET", URL: "https://example.com/orders", Status: 200}
		outgoing := incoming
		outgoing.Direction = http.Outgoing
		Expect(http.Key(incoming)).NotTo(Equal(http.Key(outgoing)))
	})

	It("decodes an agent's captured Messages response and keeps its trace correlation", func() {
		record, err := http.DecodeRecord(json.RawMessage(`{
			"startedDateTime":"2026-01-02T03:04:05Z","direction":"outgoing","traceId":"trace-example",
			"request":{"method":"POST","url":"https://api.anthropic.com/v1/messages"},
			"response":{"status":200,"content":{"mimeType":"application/json","text":"{\"model\":\"claude-test\",\"id\":\"msg-example\",\"usage\":{\"input_tokens\":0,\"output_tokens\":3}}"}}
		}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(record.TraceID).To(Equal("trace-example"))
		Expect(record.Protocol).To(Equal("anthropic"))
		Expect(record.Operation).To(Equal("messages"))
		Expect(record.Decoded.AI.ResponseID).To(Equal("msg-example"))
		Expect(record.Decoded.AI.Usage.InputTokens).To(HaveValue(Equal(int64(0))))
		Expect(record.Decoded.AI.Usage.OutputTokens).To(HaveValue(Equal(int64(3))))
		Expect(http.ToEntry(record).Metadata.Decoded).To(Equal(record.Decoded))
		Expect(strings.ReplaceAll(record.Pretty().String(), "\u00a0", " ")).To(ContainSubstring("anthropic messages"))
		Expect(record.PrettyFull().String()).To(ContainSubstring("msg-example"))
	})

	DescribeTable("rejects invalid direction values", func(direction http.Direction) {
		Expect((http.Options{Features: []string{"http"}, Selection: http.Selection{Direction: direction}}).Validate()).NotTo(Succeed())
	}, Entry("unknown", http.Direction("sideways")), Entry("blank whitespace", http.Direction(" ")))
})
