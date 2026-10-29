package tracequery

import (
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTraceQuery(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Trace query") }

var _ = Describe("trace search", func() {
	at := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	var spans []Span
	BeforeEach(func() {
		spans = []Span{
			{TraceID: "trace-a", SpanID: "root", Service: "checkout", Operation: "POST /orders", StartedAt: at, DurationMs: 100, Status: "Ok"},
			{TraceID: "trace-a", SpanID: "child", ParentID: "root", Service: "payments", Operation: "charge", StartedAt: at.Add(10 * time.Millisecond), DurationMs: 40, Status: "Error", Attributes: map[string]any{"http.status_code": 500}},
			{TraceID: "trace-b", SpanID: "other", Service: "checkout", Operation: "GET /orders", StartedAt: at.Add(time.Second), DurationMs: 10, Status: "Ok"},
		}
	})
	It("matches all span predicates on the same span but returns whole-trace duration and error totals", func() {
		result, err := Search(spans, Request{Service: "payments", Operation: "charge", Status: "error", Tags: map[string]string{"http.status_code": "500"}, MinDuration: "90ms"})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Traces).To(HaveExactElements(Summary{TraceID: "trace-a", Service: "checkout", Operation: "POST /orders", StartedAt: at, DurationMs: 100, SpanCount: 2, Errors: 1, Services: []string{"checkout", "payments"}}))
		result, err = Search(spans, Request{Service: "checkout", Operation: "charge"})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Total).To(BeZero())
	})
	It("caps traces after grouping and keeps deterministic newest-first order", func() {
		result, err := Search(spans, Request{Limit: 1})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Total).To(Equal(2))
		Expect(result.Traces).To(HaveExactElements(HaveField("TraceID", "trace-b")))
	})
	It("reports missing parents and refuses cycles or conflicting duplicate span IDs", func() {
		detail, err := Trace(spans, "trace-a")
		Expect(err).NotTo(HaveOccurred())
		Expect(detail.Spans).To(HaveLen(2))
		spans[0].ParentID = "absent"
		detail, err = Trace(spans, "trace-a")
		Expect(err).NotTo(HaveOccurred())
		Expect(detail.MissingParents).To(Equal([]string{"absent"}))
		spans[0].ParentID = "child"
		_, err = Trace(spans, "trace-a")
		Expect(err).To(MatchError(ContainSubstring("cycle")))
	})
	It("names a rootless trace after its earliest span regardless of input order", func() {
		spans[0].ParentID = "absent"
		result, err := Search([]Span{spans[1], spans[0]}, Request{})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Traces[0].Operation).To(Equal("POST /orders"))
	})
	It("deduplicates identical spans and refuses conflicting or overflowing durations", func() {
		result, err := Search(append(spans, spans[0]), Request{})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Traces[1].SpanCount).To(Equal(2))
		changed := spans[0]
		changed.DurationMs++
		_, err = Search(append(spans, changed), Request{})
		Expect(err).To(MatchError(ContainSubstring("conflicting span")))
		spans[0].DurationMs = 1e20
		_, err = Search(spans, Request{})
		Expect(err).To(MatchError(ContainSubstring("invalid span")))
	})
	DescribeTable("refuses invalid filters", func(request Request) { _, err := Search(spans, request); Expect(err).To(HaveOccurred()) },
		Entry("negative duration", Request{MinDuration: "-1ms"}), Entry("duration order", Request{MinDuration: "2s", MaxDuration: "1s"}), Entry("limit", Request{Limit: 1001}), Entry("status", Request{Status: "maybe"}))
})
