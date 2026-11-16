package jms_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/tracing/jms"
	"github.com/flanksource/telltale/sdk/protocol"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestJMS(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "JMS capture") }

var _ = Describe("JMS capture", func() {
	DescribeTable("rejects invalid capture options", func(options jms.Options) {
		Expect(options.Validate()).To(HaveOccurred())
	},
		Entry("unknown direction", jms.Options{Direction: "sideways"}),
		Entry("blank destination", jms.Options{Destinations: []string{"orders", " "}}),
		Entry("negative body limit", jms.Options{Body: true, MaxBodyBytes: -1}),
		Entry("oversized body limit", jms.Options{Body: true, MaxBodyBytes: 1<<20 + 1}),
		Entry("body limit without body capture", jms.Options{MaxBodyBytes: 100}),
		Entry("negative capacity", jms.Options{Capacity: -1}),
		Entry("oversized capacity", jms.Options{Capacity: 50001}),
	)

	It("accepts defaults and explicit incoming or outgoing selection", func() {
		for _, direction := range []string{"", "both", "incoming", "outgoing"} {
			Expect((jms.Options{Direction: direction, Destinations: []string{"orders"}}).Validate()).To(Succeed())
		}
	})

	It("retains message metadata and error phase from a checkpoint", func() {
		body := []byte(`[{"startedAt":"2026-11-14T10:00:00Z","durationMs":12.5,"operation":"process","direction":"incoming","outcome":"error","destination":"orders","redelivered":false,"transacted":true,"acknowledgeMode":0,"phase":"conversion","error":"invalid order","metadataErrors":["subscription unavailable"],"resource":{"tenant":"tenant-x"}}]`)
		rows, err := jms.Decode(agentclient.Blob{Info: protocol.Blob{ContentType: "application/json"}, Data: body}, map[string]string{"service.name": "worker"})
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(HaveLen(1))
		Expect(rows[0].StartedAt).To(Equal(time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)))
		Expect(rows[0].Resource).To(Equal(map[string]string{"service.name": "worker", "tenant": "tenant-x"}))
		Expect(rows[0].Redelivered).To(HaveValue(BeFalse()))
		Expect(rows[0].Transacted).To(HaveValue(BeTrue()))
		Expect(rows[0].AcknowledgeMode).To(HaveValue(Equal(0)))
		Expect(rows[0].Phase).To(Equal("conversion"))
		Expect(rows[0].MetadataErrors).To(Equal([]string{"subscription unavailable"}))
	})

	DescribeTable("rejects malformed or inconsistent JVM records", func(raw string) {
		_, err := jms.DecodeRecord(json.RawMessage(raw))
		Expect(err).To(HaveOccurred())
	},
		Entry("null", `null`),
		Entry("missing timestamp", `{"operation":"send","direction":"outgoing","outcome":"success"}`),
		Entry("negative duration", `{"startedAt":"2026-11-14T10:00:00Z","durationMs":-1,"operation":"send","direction":"outgoing","outcome":"success"}`),
		Entry("send declared incoming", `{"startedAt":"2026-11-14T10:00:00Z","operation":"send","direction":"incoming","outcome":"success"}`),
		Entry("unknown operation", `{"startedAt":"2026-11-14T10:00:00Z","operation":"acknowledge","direction":"incoming","outcome":"success"}`),
		Entry("error without error detail", `{"startedAt":"2026-11-14T10:00:00Z","operation":"send","direction":"outgoing","outcome":"error"}`),
		Entry("send misclassified as timeout", `{"startedAt":"2026-11-14T10:00:00Z","operation":"send","direction":"outgoing","outcome":"timeout"}`),
	)

	It("rejects null checkpoints while accepting an empty event array", func() {
		blob := agentclient.Blob{Info: protocol.Blob{ContentType: "application/json"}, Data: []byte(`null`)}
		_, err := jms.Decode(blob, nil)
		Expect(err).To(MatchError(ContainSubstring("must be an array")))
		blob.Data = []byte(`[]`)
		rows, err := jms.Decode(blob, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(BeEmpty())
	})

	It("refuses an unrelated content type", func() {
		_, err := jms.Decode(agentclient.Blob{Info: protocol.Blob{ContentType: "application/x-jfr"}, Data: []byte(`[]`)}, nil)
		Expect(err).To(MatchError(ContainSubstring("application/json")))
	})
})
