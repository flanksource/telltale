package otelreceiver

import (
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
)

func TestReceiver(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "OTLP receiver")
}

func exampleRequest() ptraceotlp.ExportRequest {
	request := ptraceotlp.NewExportRequest()
	resource := request.Traces().ResourceSpans().AppendEmpty()
	resource.SetSchemaUrl("https://opentelemetry.io/schemas/1.26.0")
	resource.Resource().Attributes().PutStr("service.name", "checkout")
	resource.Resource().Attributes().PutInt("replicas", 2)
	resource.Resource().Attributes().PutStr("password", "private-resource-value")
	scope := resource.ScopeSpans().AppendEmpty()
	scope.Scope().SetName("example-instrumentation")
	scope.Scope().SetVersion("1.0")
	scope.Scope().Attributes().PutBool("enabled", true)
	span := scope.Spans().AppendEmpty()
	span.SetTraceID(pcommon.TraceID{1})
	span.SetSpanID(pcommon.SpanID{2})
	span.SetName("POST /orders")
	span.SetKind(ptrace.SpanKindServer)
	span.SetStartTimestamp(pcommon.NewTimestampFromTime(time.Now().UTC().Add(-time.Minute)))
	span.SetEndTimestamp(span.StartTimestamp() + pcommon.Timestamp(120*time.Millisecond))
	span.SetFlags(1)
	span.TraceState().FromRaw("example=value")
	span.SetDroppedAttributesCount(3)
	span.Attributes().PutInt("large.integer", 9007199254740993)
	span.Attributes().PutStr("authorization", "private-span-value")
	event := span.Events().AppendEmpty()
	event.SetName("charged")
	event.SetTimestamp(span.StartTimestamp() + pcommon.Timestamp(time.Millisecond))
	event.Attributes().PutStr("password", "private-event-value")
	link := span.Links().AppendEmpty()
	link.SetTraceID(pcommon.TraceID{3})
	link.SetSpanID(pcommon.SpanID{4})
	link.Attributes().PutStr("token", "private-link-value")
	return request
}

var _ = Describe("OTLP span conversion", func() {
	It("preserves native metadata and typed values while redacting every attribute scope", func() {
		request := exampleRequest()
		converted, err := normalize(request, DefaultOptions())
		Expect(err).NotTo(HaveOccurred())
		Expect(converted.Rejected).To(BeZero())
		Expect(converted.Spans).To(HaveLen(1))
		span := converted.Spans[0]
		Expect(span).To(SatisfyAll(HaveField("Service", "checkout"), HaveField("DurationMs", float64(120)), HaveField("Resource", HaveKeyWithValue("replicas", int64(2)))))
		native, err := span.NativeJSON()
		Expect(err).NotTo(HaveOccurred())
		Expect(string(native)).NotTo(ContainSubstring("private-"))
		decoded := ptraceotlp.NewExportRequest()
		Expect(decoded.UnmarshalJSON(native)).To(Succeed())
		stored := decoded.Traces().ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
		Expect(stored.Events().Len()).To(Equal(1))
		Expect(stored.Links().Len()).To(Equal(1))
		Expect(stored.Flags()).To(Equal(uint32(1)))
		Expect(stored.TraceState().AsRaw()).To(Equal("example=value"))
		Expect(stored.DroppedAttributesCount()).To(Equal(uint32(3)))
		value, ok := stored.Attributes().Get("large.integer")
		Expect(ok).To(BeTrue())
		Expect(value.Int()).To(Equal(int64(9007199254740993)))
		original, err := request.MarshalJSON()
		Expect(err).NotTo(HaveOccurred())
		Expect(string(original)).To(ContainSubstring("private-span-value"))
	})
	It("rejects invalid spans individually and refuses a request above its span limit", func() {
		request := exampleRequest()
		request.Traces().ResourceSpans().At(0).ScopeSpans().At(0).Spans().AppendEmpty()
		converted, err := normalize(request, DefaultOptions())
		Expect(err).NotTo(HaveOccurred())
		Expect(converted.Spans).To(HaveLen(1))
		Expect(converted.Rejected).To(Equal(int64(1)))
		options := DefaultOptions()
		options.MaxSpans = 1
		_, err = normalize(request, options)
		Expect(err).To(HaveOccurred())
	})
	It("ignores attribute order when comparing repeated span content", func() {
		request := exampleRequest()
		first, err := normalize(request, DefaultOptions())
		Expect(err).NotTo(HaveOccurred())
		attrs := request.Traces().ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0).Attributes()
		attrs.Clear()
		attrs.PutStr("authorization", "private-span-value")
		attrs.PutInt("large.integer", 9007199254740993)
		second, err := normalize(request, DefaultOptions())
		Expect(err).NotTo(HaveOccurred())
		Expect(second.Spans[0].Digest).To(Equal(first.Spans[0].Digest))
	})
})
