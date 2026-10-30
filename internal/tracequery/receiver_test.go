package tracequery

import (
	"context"
	"time"

	"github.com/flanksource/telltale/internal/otelreceiver"
	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/credentials/apitoken"
	"github.com/flanksource/telltale/pkg/datadir"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
)

func receivedTrace(at time.Time) ptraceotlp.ExportRequest {
	request := ptraceotlp.NewExportRequest()
	resource := request.Traces().ResourceSpans().AppendEmpty()
	resource.Resource().Attributes().PutStr("service.name", "checkout")
	resource.Resource().Attributes().PutInt("replicas", 3)
	spans := resource.ScopeSpans().AppendEmpty().Spans()
	root := spans.AppendEmpty()
	root.SetTraceID(pcommon.TraceID{7})
	root.SetSpanID(pcommon.SpanID{1})
	root.SetName("order")
	root.SetStartTimestamp(pcommon.NewTimestampFromTime(at))
	root.SetEndTimestamp(root.StartTimestamp() + pcommon.Timestamp(120*time.Millisecond))
	child := spans.AppendEmpty()
	child.SetTraceID(pcommon.TraceID{7})
	child.SetSpanID(pcommon.SpanID{2})
	child.SetParentSpanID(pcommon.SpanID{1})
	child.SetName("charge")
	child.SetStartTimestamp(pcommon.NewTimestampFromTime(at.Add(20 * time.Millisecond)))
	child.SetEndTimestamp(child.StartTimestamp() + pcommon.Timestamp(60*time.Millisecond))
	child.Status().SetCode(ptrace.StatusCodeError)
	child.Attributes().PutInt("large.integer", 9007199254740993)
	return request
}

var _ = Describe("received traces through the receiver pseudo-run", func() {
	It("searches run=receiver with the window pushed down, then reads whole traces with exact native values", func() {
		ctx := context.Background()
		dir := GinkgoT().TempDir()
		store, err := otelreceiver.OpenStore(otelreceiver.StoreOptions{DataDir: dir, Writable: true, Retention: time.Hour})
		Expect(err).NotTo(HaveOccurred())
		at := time.Now().UTC().Add(-time.Minute)
		receiver, err := otelreceiver.New(otelreceiver.Config{Store: store, Options: otelreceiver.DefaultOptions(), Verifier: apitoken.NewVerifier(agentclient.NewTokenStore([]datadir.ConfigDir{{Name: datadir.ConfigGlobal, Dir: dir}}).Lookup)})
		Expect(err).NotTo(HaveOccurred())
		response, err := receiver.Export(ctx, receivedTrace(at))
		Expect(err).NotTo(HaveOccurred())
		Expect(response.PartialSuccess().RejectedSpans()).To(BeZero())
		Expect(receiver.Stop(ctx)).To(Succeed())
		Expect(store.Close()).To(Succeed())

		service := Service{DataDir: dir}
		window := Request{Source: "records", Run: otelreceiver.SourceID, From: at.Add(10 * time.Millisecond).Format(time.RFC3339Nano), Until: at.Add(time.Second).Format(time.RFC3339Nano)}
		window.Operation, window.Status, window.MinDuration = "charge", "error", "100ms"
		result, err := service.Search(ctx, window)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Traces).To(HaveExactElements(And(HaveField("SpanCount", 2), HaveField("DurationMs", float64(120)), HaveField("Errors", 1))))
		detail, err := service.Trace(ctx, Request{Source: "records", Run: otelreceiver.SourceID}, pcommon.TraceID{7}.String())
		Expect(err).NotTo(HaveOccurred())
		Expect(detail.Spans).To(HaveLen(2))
		Expect(detail.Spans).To(HaveEach(SatisfyAll(HaveField("Resource", HaveKeyWithValue("replicas", int64(3))), HaveField("OTLP", Not(BeEmpty())))))
		Expect(detail.Spans).To(ContainElement(HaveField("Attributes", HaveKeyWithValue("large.integer", int64(9007199254740993)))))
	})
	It("offers no special receiver source", func() {
		Expect(Service{DataDir: GinkgoT().TempDir()}.load(context.Background(), Request{Source: otelreceiver.SourceID}, nil)).Error().To(HaveOccurred())
	})
})
