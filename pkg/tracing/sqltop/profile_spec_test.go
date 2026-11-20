package sqltop

import (
	"context"
	"encoding/json"
	"time"

	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/otlp/profiles"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/tracingtest"
	"github.com/flanksource/telltale/sdk/protocol"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("SQL top JVM profile projection", func() {
	DescribeTable("requires authenticated agent identities before decoding profiles", func(capture, source, reason string) {
		env := tracingtest.NewEnv(map[string]tracing.TracePlugin{"sql_top": Kind()})
		store, err := newActiveStore(env.Results.Backend, "sql-profile-source")
		Expect(err).NotTo(HaveOccurred())
		ctx := context.WithValue(context.Background(), activeStoreKey{}, store)
		data, err := profiles.Encode([]profiles.Snapshot{{Kind: "sql_top", CaptureID: "capture", Source: "java-instance", ProfileID: "0123456789abcdef0123456789abcdef", Sequence: 1, StartedAt: serverTime, CompletedAt: serverTime.Add(time.Second), Limit: 50, Complete: true}})
		Expect(err).NotTo(HaveOccurred())
		err = agentCapture(Params{}).HandleBlob(ctx, agentclient.Blob{Info: protocol.Blob{ContentType: profiles.ContentType, Session: capture}, Data: data}, map[string]string{"telltale.source.instance": source}, nil)
		Expect(err).To(MatchError(ContainSubstring(reason)))
	}, Entry("missing source", "capture", "", "agent Hello is missing telltale.source.instance"), Entry("missing capture", "", "java-instance", "missing the authenticated capture session"))

	It("preserves the producer limit instead of replacing it with the local default", func() {
		env := tracingtest.NewEnv(map[string]tracing.TracePlugin{"sql_top": Kind()})
		store, err := newActiveStore(env.Results.Backend, "sql-profile-controls")
		Expect(err).NotTo(HaveOccurred())
		ctx := context.WithValue(context.Background(), activeStoreKey{}, store)
		const producerLimit = 7
		data, err := profiles.Encode([]profiles.Snapshot{{Kind: "sql_top", CaptureID: "capture", Source: "java-instance", ProfileID: "0123456789abcdef0123456789abcdef", Sequence: 1, StartedAt: serverTime, CompletedAt: serverTime.Add(time.Second), Limit: producerLimit, Complete: true}})
		Expect(err).NotTo(HaveOccurred())
		Expect(agentCapture(Params{}).HandleBlob(ctx, agentclient.Blob{Info: protocol.Blob{ContentType: profiles.ContentType, Session: "capture"}, Data: data}, map[string]string{"telltale.source.instance": "java-instance"}, nil)).To(Succeed())
		frames := env.Read("sql_top_frames", "sql-profile-controls-frames", query.PageRequest{Limit: 10}, nil)
		Expect(frames).To(HaveLen(1))
		Expect(frames[0]).To(HaveKeyWithValue("limit", BeNumerically("==", producerLimit)))
	})

	It("keeps producer times, identity, correlation and measured API lifetime", func() {
		snapshot := profiles.Snapshot{Kind: "sql_top", CaptureID: "capture", Source: "java-instance", Rows: []profiles.Row{{OperationID: "execution", ElapsedNS: int64(2 * time.Millisecond), SampledAt: serverTime, TraceID: "trace", SpanID: "span", Attributes: map[string]any{"startedAt": serverTime.Add(-2 * time.Millisecond).Format(time.RFC3339Nano), "api": "r2dbc", "sql": "SELECT id FROM orders"}}}}
		rows, err := FromSnapshot(snapshot)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(Equal([]ActiveSample{{Source: "java-instance/capture", OperationID: "execution", StartedAt: serverTime.Add(-2 * time.Millisecond), SampledAt: serverTime, ElapsedMs: 2, API: "r2dbc", SQL: "SELECT id FROM orders", TraceID: "trace", SpanID: "span"}}))
	})

	It("retains missing SQL as a diagnostic and rejects silently absent metadata", func() {
		snapshot := profiles.Snapshot{Kind: "sql_top", Rows: []profiles.Row{{Attributes: map[string]any{"startedAt": serverTime.Format(time.RFC3339), "api": "jdbc"}}}}
		_, err := FromSnapshot(snapshot)
		Expect(err).To(MatchError(ContainSubstring("SQL text or unavailable reason")))
		snapshot.Rows[0].Attributes["sqlUnavailableReason"] = "prepared-before-instrumentation"
		rows, err := FromSnapshot(snapshot)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows[0].SQLUnavailableReason).To(Equal("prepared-before-instrumentation"))
	})

	It("preserves the active phase and bounded-query metadata", func() {
		snapshot := profiles.Snapshot{Kind: "sql_top", Rows: []profiles.Row{{Attributes: map[string]any{"startedAt": serverTime.Format(time.RFC3339), "api": "r2dbc", "sql": "SELECT id FROM orders", "queryFingerprint": "normalized-query-hash", "sqlTruncated": true, "phase": "awaiting-consumption"}}}}
		rows, err := FromSnapshot(snapshot)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows[0].Phase).To(Equal("awaiting-consumption"))
		Expect(rows[0].QueryFingerprint).To(Equal("normalized-query-hash"))
		Expect(rows[0].SQLTruncated).To(BeTrue())
	})

	It("translates normal polling options to runtime capture milliseconds and limits", func() {
		data, err := agentArmParams(Params{Options: Options{Poll: "250ms", Top: 12}})
		Expect(err).NotTo(HaveOccurred())
		var params map[string]any
		Expect(json.Unmarshal(data, &params)).To(Succeed())
		Expect(params).To(Equal(map[string]any{"pollIntervalMs": float64(250), "limit": float64(12)}))
	})

	It("passes the parsed minimum elapsed duration to the Java boundary", func() {
		data, err := agentArmParams(Params{Options: Options{MinElapsed: "100ms"}})
		Expect(err).NotTo(HaveOccurred())
		var params map[string]any
		Expect(json.Unmarshal(data, &params)).To(Succeed())
		Expect(params).To(HaveKeyWithValue("minElapsedNs", float64(100*time.Millisecond)))
	})
})
