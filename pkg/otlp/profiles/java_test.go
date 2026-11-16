package profiles_test

import (
	"os"
	"time"

	"github.com/flanksource/telltale/pkg/otlp/profiles"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Java Profiles golden wire contract", func() {
	DescribeTable("decodes official Java ProfilesData without JSON integer loss", func(name string, sequence int64, elapsed int64) {
		data, err := os.ReadFile("testdata/java-http-top-" + name + ".pb")
		Expect(err).NotTo(HaveOccurred())
		decoded, err := profiles.Decode(data, profiles.Options{ExpectedKind: "http_top", ExpectedSource: "jvm-a", ExpectedCaptureID: "capture-a"})
		Expect(err).NotTo(HaveOccurred())
		Expect(decoded).To(HaveLen(1))
		snapshot := decoded[0]
		Expect(snapshot.Sequence).To(Equal(sequence))
		Expect(snapshot.Complete).To(BeTrue())
		Expect(snapshot.PollIntervalMS).To(Equal(int64(5000)))
		if name == "empty" {
			Expect(snapshot.Rows).To(BeEmpty())
			Expect(snapshot.Observed).To(BeZero())
			return
		}
		Expect(snapshot.Resource).To(HaveKeyWithValue("service.name", "checkout"))
		Expect(snapshot.Rows).To(ConsistOf(profiles.Row{OperationID: "request-a", ElapsedNS: elapsed,
			SampledAt:  time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC),
			Attributes: map[string]any{"method": "GET", "url": "https://example.test/orders", "route": "/orders/{id}", "direction": "incoming", "startedAt": "2026-11-14T11:59:59Z", "threadId": int64(9007199254740993)},
			TraceID:    "0102030405060708090a0b0c0d0e0f10", SpanID: "0102030405060708", Stack: []string{"application.handle", "application.run"}}))
	}, Entry("active", "active", int64(1), int64(250000000)), Entry("updated", "updated", int64(2), int64(500000000)), Entry("empty", "empty", int64(3), int64(0)))
})
