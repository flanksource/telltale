package tracequery

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/traces/read"
	"github.com/flanksource/telltale/pkg/tracing"
	sdk "github.com/flanksource/telltale/sdk"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func recordedSpan(span sdk.Span) sdk.Span {
	GinkgoHelper()
	Expect(span.Normalize()).To(Succeed())
	return span
}

var _ = Describe("recorded trace search", func() {
	It("queries SDK and OpenSearch spans through one record and reads the full trace outside the search window", func() {
		ctx := context.Background()
		dir := GinkgoT().TempDir()
		at := time.Now().UTC().Add(-time.Minute)
		host, err := traces.Open(traces.OpenOptions{DataDir: dir, RunID: "run-search"})
		Expect(err).NotTo(HaveOccurred())
		sdkSource := sdk.SpanSource{Type: sdk.SpanSourceSDK}
		_, err = recordstore.AppendTyped(ctx, host.Runtime().Results.Backend, "sdk-spans", "spans", []sdk.Span{
			recordedSpan(sdk.Span{TraceID: "trace-a", SpanID: "root", Operation: "checkout", StartedAt: at, DurationMs: 100, Status: "ok", Resource: map[string]any{"service.name": "store"}, Source: sdkSource}),
			recordedSpan(sdk.Span{TraceID: "trace-a", SpanID: "child", ParentID: "root", Operation: "charge", StartedAt: at.Add(20 * time.Millisecond), DurationMs: 40, Status: "error", Resource: map[string]any{"service.name": "payments"}, Attributes: map[string]any{"order.id": "example"}, Source: sdkSource}),
		})
		Expect(err).NotTo(HaveOccurred())
		_, err = recordstore.AppendTyped(ctx, host.Runtime().Results.Backend, "imported-spans", "spans", []sdk.Span{
			recordedSpan(sdk.Span{TraceID: "trace-b", SpanID: "root-b", Service: "search", Operation: "lookup", StartedAt: at, DurationMs: 10, Source: sdk.SpanSource{Type: sdk.SpanSourceOpenSearch, Index: "index", ID: "1"}}),
		})
		Expect(err).NotTo(HaveOccurred())
		info := telltale.Result{RunID: host.RunID(), Dir: host.Dir(), StartedAt: at, EndedAt: time.Now(), Captures: []telltale.CaptureResult{
			{Kind: "spans", Ref: tracing.TraceRef{Kind: "spans", Stream: "sdk-spans"}}, {Kind: "spans", Ref: tracing.TraceRef{Kind: "spans", Stream: "imported-spans"}},
		}}
		data, err := json.Marshal(info)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(host.Dir(), telltale.ManifestFile), data, 0600)).To(Succeed())
		Expect(host.Close(ctx)).To(Succeed())
		service := Service{DataDir: dir}
		result, err := service.Search(ctx, Request{Source: "records", From: at.Add(10 * time.Millisecond).Format(time.RFC3339Nano), Until: at.Add(time.Second).Format(time.RFC3339Nano), Services: Modes{"payments": Include}})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Traces).To(HaveExactElements(And(HaveField("TraceID", "trace-a"), HaveField("SpanCount", 2), HaveField("DurationMs", float64(100)), HaveField("Errors", 1))))
		all, err := service.Search(ctx, Request{Source: "records", Lookback: "1h"})
		Expect(err).NotTo(HaveOccurred())
		Expect(all.Total).To(Equal(2))
		detail, err := service.Trace(ctx, Request{Source: "records"}, "trace-a")
		Expect(err).NotTo(HaveOccurred())
		Expect(detail.Spans).To(HaveLen(2))
		Expect(detail.Spans[1].Attributes).To(HaveKeyWithValue("order.id", "example"))
	})
	It("pushes the window, or the trace IDs a search found, down to the store", func() {
		from, until := "2026-10-05T09:00:00Z", "2026-10-05T10:00:00.5Z"
		windowed, err := recordedQuery(Request{From: from, Until: until}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(windowed).To(Equal(read.Query{All: true, From: time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC), Until: time.Date(2026, 10, 5, 10, 0, 0, 5e8, time.UTC)}))
		traced, err := recordedQuery(Request{}, []string{"trace-a", "trace-b"})
		Expect(err).NotTo(HaveOccurred())
		Expect(traced).To(Equal(read.Query{All: true, Filters: []string{"traceId=trace-a,trace-b"}}))
		_, err = recordedQuery(Request{From: "yesterday"}, nil)
		Expect(err).To(MatchError(ContainSubstring("from")))
	})
})
