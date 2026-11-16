package sql

import (
	"encoding/json"
	"time"

	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/sdk/protocol"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gstruct"
)

var _ = Describe("JVM SQL records", func() {
	It("passes parsed duration limits to the JVM including an explicit zero", func() {
		for _, duration := range []string{"0s", "1ms"} {
			params, err := agentArmParams(Params{Options: Options{Connections: []string{"*"}, MinDuration: duration}})
			Expect(err).NotTo(HaveOccurred())
			var values map[string]any
			Expect(json.Unmarshal(params, &values)).To(Succeed())
			parsed, err := time.ParseDuration(duration)
			Expect(err).NotTo(HaveOccurred())
			Expect(values).To(HaveKeyWithValue("minDurationNs", float64(parsed)))
		}
	})
	It("preserves observed JDBC metadata without inventing row counts", func() {
		row, err := decodeAgentStatement(json.RawMessage(`{"startedAt":"2026-11-14T10:00:00Z","durationNs":2500000,"sql":"SELECT id FROM orders WHERE id = ?","driver":"jdbc","connection":"orders","operationId":"operation-1","traceId":"trace-1","spanId":"span-1","api":"jdbc","batchSize":2,"database":"orders","engine":"h2"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(row.StartedAt).To(Equal(time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)))
		Expect(row.DurationMs).To(Equal(2.5))
		Expect(row.Rows).To(Equal(int64(-1)))
		Expect(row.RepeatKey).NotTo(BeEmpty())
		data, err := json.Marshal(row)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(ContainSubstring(`"operationId":"operation-1"`))
		Expect(string(data)).To(ContainSubstring(`"batchSize":2`))
		Expect(string(data)).To(ContainSubstring(`"database":"orders"`))
		Expect(string(data)).To(ContainSubstring(`"engine":"h2"`))
	})

	It("keeps missing prepared SQL explicit and out of repeated-query groups", func() {
		row, err := decodeAgentStatement(json.RawMessage(`{"startedAt":"2026-11-14T10:00:00Z","durationNs":1000,"operationId":"old-prepared","api":"jdbc","sqlUnavailableReason":"prepared-before-instrumentation"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(row.SQL).To(BeEmpty())
		Expect(row.RepeatKey).To(BeEmpty())
		Expect(row.Rows).To(Equal(int64(-1)))
		Expect(Summarize([]StatementRow{row, row}).Repeated).To(BeZero())
	})

	It("preserves a capture-ended reactive execution as explicitly incomplete", func() {
		row, err := decodeAgentStatement(json.RawMessage(`{"startedAt":"2026-11-14T10:00:00Z","durationNs":1000,"operationId":"unconsumed","api":"r2dbc","sql":"SELECT id FROM orders","phase":"awaiting-consumption","incomplete":true,"completion":"capture-ended"}`))
		Expect(err).NotTo(HaveOccurred())
		data, err := json.Marshal(row)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(ContainSubstring(`"phase":"awaiting-consumption"`))
		Expect(string(data)).To(ContainSubstring(`"incomplete":true`))
		Expect(string(data)).To(ContainSubstring(`"completion":"capture-ended"`))
	})

	DescribeTable("rejects malformed JVM observations", func(raw string) {
		_, err := decodeAgentStatement(json.RawMessage(raw))
		Expect(err).To(HaveOccurred())
	},
		Entry("missing execution time", `{"api":"jdbc","operationId":"one","sql":"SELECT 1"}`),
		Entry("negative duration", `{"api":"jdbc","operationId":"one","startedAt":"2026-11-14T10:00:00Z","durationNs":-1,"sql":"SELECT 1"}`),
		Entry("missing SQL without a reason", `{"api":"jdbc","operationId":"one","startedAt":"2026-11-14T10:00:00Z","durationNs":1}`),
	)
})

var _ = Describe("JVM SQL checkpoint blobs", func() {
	It("wires checkpoint and stop decoding through the SQL capture seam", func() {
		capture, ok := NewCapture(nil).(statements)
		Expect(ok).To(BeTrue())
		Expect(capture.agent.DecodeBlob).NotTo(BeNil())
		decoded, err := capture.agent.DecodeBlob(agentclient.Blob{Info: protocol.Blob{ContentType: "application/json"}, Data: []byte(`[]`)}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(decoded.Rows).To(BeEmpty())
	})

	It("decodes JDBC and reactive completion metadata with the attached JVM resource", func() {
		blob := agentclient.Blob{Info: protocol.Blob{ContentType: "application/json"}, Data: []byte(`[
			{"startedAt":"2026-11-14T10:00:00Z","durationNs":2500000,"sql":"SELECT id FROM orders","operationId":"jdbc-query","api":"jdbc","rows":2},
			{"startedAt":"2026-11-14T10:00:01Z","durationNs":5000000,"sql":"SELECT id FROM orders","operationId":"reactive-query","api":"r2dbc","phase":"awaiting-consumption","incomplete":true,"completion":"capture-ended"}
		]`)}
		resource := map[string]string{"service.name": "orders", "process.pid": "17"}
		decoded, err := decodeAgentBlob(blob, resource)
		Expect(err).NotTo(HaveOccurred())
		Expect(decoded.Rows).To(HaveExactElements(
			gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
				"Observation": gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{"OperationID": Equal("jdbc-query"), "API": Equal("jdbc")}),
				"Rows":        Equal(int64(2)), "DurationMs": Equal(2.5), "Resource": Equal(resource),
			}),
			gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
				"Observation": gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{"OperationID": Equal("reactive-query"), "API": Equal("r2dbc"), "Incomplete": BeTrue(), "Completion": Equal("capture-ended")}),
				"Rows":        Equal(int64(-1)), "DurationMs": Equal(float64(5)), "Resource": Equal(resource),
			}),
		))
	})

	It("accepts an explicitly empty checkpoint array", func() {
		decoded, err := decodeAgentBlob(agentclient.Blob{Info: protocol.Blob{ContentType: "application/json"}, Data: []byte(`[]`)}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(decoded.Rows).To(BeEmpty())
	})

	DescribeTable("rejects invalid checkpoint payloads without returning partial rows", func(contentType, payload string) {
		decoded, err := decodeAgentBlob(agentclient.Blob{Info: protocol.Blob{ContentType: contentType}, Data: []byte(payload)}, nil)
		Expect(err).To(HaveOccurred())
		Expect(decoded.Rows).To(BeEmpty())
	},
		Entry("unrelated content type", "application/x-jfr", `[]`),
		Entry("malformed JSON", "application/json", `[`),
		Entry("null instead of event array", "application/json", `null`),
		Entry("object instead of event array", "application/json", `{}`),
		Entry("null event", "application/json", `[null]`),
		Entry("missing execution fields", "application/json", `[{"api":"jdbc","operationId":"query","sql":"SELECT 1"}]`),
		Entry("untyped event", "application/json", `[{}]`),
		Entry("invalid event following valid SQL", "application/json", `[{"startedAt":"2026-11-14T10:00:00Z","durationNs":1000,"sql":"SELECT 1","operationId":"query","api":"jdbc"},null]`),
	)
})
