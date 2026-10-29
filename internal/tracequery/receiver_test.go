package tracequery

import (
	"context"
	"encoding/base64"
	"time"

	"github.com/flanksource/telltale/internal/otelreceiver"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
)

var _ = Describe("received trace search", func() {
	It("loads complete traces beyond the search window and remains readable after reopening", func() {
		ctx := context.Background()
		dir := GinkgoT().TempDir()
		store, err := otelreceiver.OpenStore(otelreceiver.StoreOptions{WorkDir: dir, Writable: true, Retention: time.Hour})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(store.Close)
		request := ptraceotlp.NewExportRequest()
		resource := request.Traces().ResourceSpans().AppendEmpty()
		resource.Resource().Attributes().PutInt("replicas", 3)
		resource.ScopeSpans().AppendEmpty().Spans().AppendEmpty()
		native, err := request.MarshalProto()
		Expect(err).NotTo(HaveOccurred())
		at := time.Now().UTC().Add(-time.Minute)
		_, err = store.Persist(ctx, []otelreceiver.Span{
			{Key: "trace/root", TraceID: "trace", SpanID: "root", Service: "checkout", Operation: "order", StartedAt: at, DurationMs: 120, Digest: "root", Native: base64.StdEncoding.EncodeToString(native), Resource: map[string]any{"replicas": 3}},
			{Key: "trace/child", TraceID: "trace", SpanID: "child", ParentID: "root", Service: "payments", Operation: "charge", StartedAt: at.Add(20 * time.Millisecond), DurationMs: 60, Status: "Error", Digest: "child", Native: base64.StdEncoding.EncodeToString(native)},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(store.Close()).To(Succeed())
		service := Service{WorkDir: dir}
		result, err := service.Search(ctx, Request{Source: "receiver", From: at.Add(10 * time.Millisecond).Format(time.RFC3339Nano), Until: at.Add(time.Second).Format(time.RFC3339Nano), Operation: "charge", Status: "error", MinDuration: "100ms"})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Traces).To(HaveExactElements(And(HaveField("SpanCount", 2), HaveField("DurationMs", float64(120)))))
		detail, err := service.Trace(ctx, Request{Source: "receiver"}, "trace")
		Expect(err).NotTo(HaveOccurred())
		Expect(detail.Spans).To(HaveLen(2))
		Expect(detail.Spans[0].Resource).To(HaveKey("replicas"))
		_, err = service.Search(ctx, Request{Source: "receiver", Index: "unexpected"})
		Expect(err).To(MatchError(ContainSubstring("does not accept")))
	})
})
