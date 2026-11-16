package main

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/flanksource/telltale/pkg/otlp/profiles"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestDemoChecks(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "JVM demo evidence")
}

func snapshotFixture(kind string, sequence int64, rows []profiles.Row) topSnapshot {
	GinkgoHelper()
	at := time.Date(2026, time.November, 15, 12, 0, 0, 0, time.UTC)
	snapshot := profiles.Snapshot{Kind: kind, CaptureID: "demo-capture", Source: "demo-jvm",
		ProfileID: "97fa6305-a036-4d96-8789-37c1bbe378f3", Sequence: sequence,
		StartedAt: at, CompletedAt: at.Add(time.Second), Complete: true, Limit: 50, Observed: len(rows), Rows: rows}
	for i := range snapshot.Rows {
		snapshot.Rows[i].SampledAt = snapshot.CompletedAt
	}
	native, err := profiles.Encode([]profiles.Snapshot{snapshot})
	Expect(err).NotTo(HaveOccurred())
	projected := make([]map[string]any, len(rows))
	for i, row := range rows {
		projected[i] = map[string]any{"operationId": row.OperationID}
	}
	return topSnapshot{Frame: topFrame{ID: snapshot.ProfileID, Tick: sequence,
		Summary: map[string]any{"otlp": base64.StdEncoding.EncodeToString(native)}}, Rows: projected, Count: len(rows)}
}

func httpRows(elapsed int64) []profiles.Row {
	return []profiles.Row{
		{OperationID: "http-client", ElapsedNS: elapsed, Attributes: map[string]any{"direction": "outgoing", "url": "http://127.0.0.1/demo/hold", "method": "GET"}},
		{OperationID: "http-server", ElapsedNS: elapsed, Attributes: map[string]any{"direction": "incoming", "url": "http://127.0.0.1/demo/hold", "route": "/demo/{id}", "handler": "DemoController#demo", "method": "GET"}},
	}
}

var _ = Describe("active operation evidence", func() {
	It("requires both incoming and outgoing HTTP operations", func() {
		Expect(checkActive("http_top", snapshotFixture("http_top", 1, httpRows(1000)))).To(Succeed())
		Expect(checkActive("http_top", snapshotFixture("http_top", 1, httpRows(1000)[:1]))).To(MatchError(ContainSubstring("incoming")))
	})

	It("requires JDBC and R2DBC operations for the demo query", func() {
		rows := []profiles.Row{
			{OperationID: "jdbc-query", ElapsedNS: 1000, Attributes: map[string]any{"api": "jdbc", "sql": "SELECT DEMO_HOLD('jdbc')"}},
			{OperationID: "r2dbc-query", ElapsedNS: 1000, Attributes: map[string]any{"api": "r2dbc", "sql": "SELECT DEMO_HOLD('r2dbc')"}},
		}
		Expect(checkActive("sql_top", snapshotFixture("sql_top", 1, rows))).To(Succeed())
		Expect(checkActive("sql_top", snapshotFixture("sql_top", 1, rows[:1]))).To(MatchError(ContainSubstring("r2dbc")))
	})

	It("rejects changing operation identities and non-increasing elapsed values", func() {
		previous := snapshotFixture("http_top", 1, httpRows(1000))
		Expect(checkUpdated(previous, snapshotFixture("http_top", 2, httpRows(2000)))).To(Succeed())
		Expect(checkUpdated(previous, snapshotFixture("http_top", 1, httpRows(2000)))).To(MatchError(ContainSubstring("sequence")))
		Expect(checkUpdated(previous, snapshotFixture("http_top", 2, httpRows(1000)))).To(MatchError(ContainSubstring("elapsed")))
		changed := httpRows(2000)
		changed[0].OperationID = "replacement-client"
		Expect(checkUpdated(previous, snapshotFixture("http_top", 2, changed))).To(MatchError(ContainSubstring("http-client")))
	})

	It("requires a native empty control snapshot and zero projected rows", func() {
		empty := snapshotFixture("http_top", 3, nil)
		Expect(checkCleared(empty)).To(Succeed())
		Expect(checkCleared(snapshotFixture("http_top", 3, httpRows(3000)))).To(MatchError(ContainSubstring("empty")))
		empty.Rows = []map[string]any{{"operationId": "phantom"}}
		Expect(checkCleared(empty)).To(MatchError(ContainSubstring("projected")))
	})

	It("rejects missing native Profiles evidence", func() {
		_, err := decodeSnapshot(topSnapshot{})
		Expect(err).To(MatchError(ContainSubstring("native")))
	})
})

func completedFixture() completedEvidence {
	return completedEvidence{
		HTTP: []httpRecord{
			{Direction: "incoming", URL: "http://127.0.0.1/demo/hold", Route: "/demo/{id}", Handler: "DemoController#demo", Status: 200},
			{Direction: "outgoing", URL: "http://127.0.0.1/demo/hold", Status: 200},
		},
		SQL: []sqlRecord{{API: "jdbc", SQL: "SELECT DEMO_HOLD('jdbc')"}, {API: "r2dbc", SQL: "SELECT DEMO_HOLD('r2dbc')"}},
		JMS: []jmsRecord{
			{Direction: "outgoing", Destination: "demo.orders", MessageID: "message-7", Outcome: "success"},
			{Direction: "incoming", Destination: "demo.orders", MessageID: "message-7", Framework: "spring-jms", Outcome: "success"},
		},
		Mirrored: []spanRecord{{TraceID: "trace-7", SpanID: "span-7", Operation: "jvm-demo-cycle", DurationMs: 1000}},
		Received: []spanRecord{{TraceID: "trace-7", SpanID: "span-7", Operation: "jvm-demo-cycle", DurationMs: 1000}},
	}
}

var _ = Describe("completed protocol evidence", func() {
	It("accepts successful Spring protocol observations with preserved exporter delivery", func() {
		Expect(checkCompleted(completedFixture())).To(Succeed())
	})

	DescribeTable("rejects missing or changed protocol evidence", func(change func(*completedEvidence), message string) {
		evidence := completedFixture()
		change(&evidence)
		Expect(checkCompleted(evidence)).To(MatchError(ContainSubstring(message)))
	},
		Entry("missing outgoing HTTP", func(e *completedEvidence) { e.HTTP = e.HTTP[:1] }, "outgoing"),
		Entry("unmatched Spring route", func(e *completedEvidence) { e.HTTP[0].Route = "" }, "route"),
		Entry("missing reactive SQL", func(e *completedEvidence) { e.SQL = e.SQL[:1] }, "r2dbc"),
		Entry("unmatched JMS message", func(e *completedEvidence) { e.JMS[1].MessageID = "different-message" }, "message"),
		Entry("lost exporter span", func(e *completedEvidence) { e.Received = nil }, "exporter"),
		Entry("changed exporter span", func(e *completedEvidence) { e.Received[0].DurationMs++ }, "changed"),
	)
})
