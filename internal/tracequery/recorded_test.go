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
	"github.com/flanksource/telltale/pkg/tracing"
	imported "github.com/flanksource/telltale/pkg/tracing/opensearch"
	sdk "github.com/flanksource/telltale/sdk"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("recorded trace search", func() {
	It("queries both SDK and imported spans and reads the full trace outside the search window", func() {
		ctx := context.Background()
		dir := GinkgoT().TempDir()
		at := time.Now().UTC().Add(-time.Minute)
		host, err := traces.Open(traces.OpenOptions{WorkDir: dir, RunID: "run-search"})
		Expect(err).NotTo(HaveOccurred())
		_, err = recordstore.AppendTyped(ctx, host.Runtime().Results.Backend, "sdk-spans", "spans", []sdk.Span{
			{TraceID: "trace-a", SpanID: "root", Name: "checkout", StartedAt: at, DurationMs: 100, Status: "Ok", Resource: map[string]string{"service.name": "store"}},
			{TraceID: "trace-a", SpanID: "child", ParentID: "root", Name: "charge", StartedAt: at.Add(20 * time.Millisecond), DurationMs: 40, Status: "Error", Resource: map[string]string{"service.name": "payments"}, Attrs: map[string]any{"order.id": "example"}},
		})
		Expect(err).NotTo(HaveOccurred())
		_, err = recordstore.AppendTyped(ctx, host.Runtime().Results.Backend, "imported-spans", "opensearch", []imported.Span{{TraceID: "trace-b", SpanID: "root-b", Service: "search", Operation: "lookup", Timestamp: at, DurationMs: 10, SourceKey: "index/1"}})
		Expect(err).NotTo(HaveOccurred())
		info := telltale.Result{RunID: host.RunID(), Dir: host.Dir(), StartedAt: at, EndedAt: time.Now(), Captures: []telltale.CaptureResult{
			{Kind: "spans", Ref: tracing.TraceRef{Kind: "spans", Stream: "sdk-spans"}}, {Kind: "opensearch", Ref: tracing.TraceRef{Kind: "opensearch", Stream: "imported-spans"}},
		}}
		data, err := json.Marshal(info)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(host.Dir(), telltale.ManifestFile), data, 0600)).To(Succeed())
		Expect(host.Close(ctx)).To(Succeed())
		service := Service{WorkDir: dir}
		result, err := service.Search(ctx, Request{Source: "records", From: at.Add(10 * time.Millisecond).Format(time.RFC3339Nano), Until: at.Add(time.Second).Format(time.RFC3339Nano), Service: "payments"})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Traces).To(HaveExactElements(And(HaveField("TraceID", "trace-a"), HaveField("SpanCount", 2), HaveField("DurationMs", float64(100)))))
		all, err := service.Search(ctx, Request{Source: "records", Lookback: "1h"})
		Expect(err).NotTo(HaveOccurred())
		Expect(all.Total).To(Equal(2))
		detail, err := service.Trace(ctx, Request{Source: "records"}, "trace-a")
		Expect(err).NotTo(HaveOccurred())
		Expect(detail.Spans).To(HaveLen(2))
		Expect(detail.Spans[1].Attributes).To(HaveKeyWithValue("order.id", "example"))
	})
})
