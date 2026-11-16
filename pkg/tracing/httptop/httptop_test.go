package httptop_test

import (
	"testing"
	"time"

	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/otlp/profiles"
	"github.com/flanksource/telltale/pkg/tracing/httptop"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestHTTPTop(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "HTTP top") }

var _ = Describe("HTTP profiles", func() {
	It("decodes an authoritative empty snapshot to clear completed requests", func() {
		start := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		data, err := profiles.Encode([]profiles.Snapshot{{Kind: httptop.Kind, CaptureID: "capture", Source: "jvm", ProfileID: "0123456789abcdef0123456789abcdef", Sequence: 2, StartedAt: start, CompletedAt: start.Add(time.Second), Limit: 50, Complete: true}})
		Expect(err).NotTo(HaveOccurred())
		blob := agentclient.Blob{Data: data}
		blob.Info.ContentType, blob.Info.Session = profiles.ContentType, "capture"
		decoded, err := httptop.Decode(blob, map[string]string{"telltale.source.instance": "jvm"})
		Expect(err).NotTo(HaveOccurred())
		Expect(decoded.Rows).To(BeEmpty())
		Expect(decoded.Frame).NotTo(BeNil())
		Expect(decoded.Frame.Source).To(Equal("jvm/capture"))
		Expect(decoded.Frame.Tick).To(Equal(int64(2)))
	})
	DescribeTable("rejects invalid sample controls", func(options httptop.Options) { Expect(options.Validate()).NotTo(Succeed()) }, Entry("zero poll", httptop.Options{Poll: "0s"}), Entry("negative limit", httptop.Options{Top: -1}), Entry("negative age", httptop.Options{MinElapsed: "-1s"}))
	It("projects authoritative source and operation identity without rounding elapsed time", func() {
		started := time.Date(2026, 1, 2, 3, 4, 5, 123, time.UTC)
		snapshot := profiles.Snapshot{Kind: httptop.Kind, Source: "jvm", CaptureID: "capture", Resource: map[string]any{"service.name": "example"}, Rows: []profiles.Row{{OperationID: "request-7", ElapsedNS: 1234567, SampledAt: started.Add(time.Second), Attributes: map[string]any{"startedAt": started.Format(time.RFC3339Nano), "source": "untrusted", "requestId": "untrusted", "direction": "incoming", "method": "GET", "route": "/orders/{id}", "handler": "example.OrderController#get", "sampled": false}, TraceID: "0123456789abcdef0123456789abcdef", SpanID: "0123456789abcdef"}}}
		rows, err := httptop.FromSnapshot(snapshot)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(HaveLen(1))
		Expect(rows[0].Source).To(Equal("jvm/capture"))
		Expect(rows[0].RequestID).To(Equal("request-7"))
		Expect(rows[0].Handler).To(Equal("example.OrderController#get"))
		Expect(rows[0].StartedAt).To(Equal(started))
		Expect(rows[0].ElapsedMs).To(Equal(1.234567))
		Expect(rows[0].Sampled).NotTo(BeNil())
		Expect(*rows[0].Sampled).To(BeFalse())
		Expect(rows[0].Resource).To(Equal(map[string]string{"service.name": "example"}))
	})

	DescribeTable("binds agent profiles to the armed capture and JVM source", func(capture, source string) {
		start := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		data, err := profiles.Encode([]profiles.Snapshot{{Kind: httptop.Kind, CaptureID: "capture", Source: "jvm", ProfileID: "0123456789abcdef0123456789abcdef", Sequence: 1, StartedAt: start, CompletedAt: start.Add(time.Second), Limit: 50, Complete: true}})
		Expect(err).NotTo(HaveOccurred())
		blob := agentclient.Blob{Data: data}
		blob.Info.ContentType, blob.Info.Session = profiles.ContentType, capture
		_, err = httptop.Decode(blob, map[string]string{"telltale.source.instance": source})
		Expect(err).To(HaveOccurred())
	}, Entry("foreign capture", "foreign", "jvm"), Entry("foreign source", "capture", "foreign"), Entry("missing capture", "", "jvm"), Entry("missing source", "capture", ""))
})
