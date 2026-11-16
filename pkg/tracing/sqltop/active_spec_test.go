package sqltop

import (
	"encoding/json"
	"time"

	"github.com/flanksource/telltale/pkg/tracing/target"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("SQL active projection", func() {
	It("preserves database identities and observed blockers in a common top row", func() {
		rows := projectDatabase("connection://orders", []Sample{{SampledAt: serverTime, StartedAt: serverTime.Add(-time.Second), SessionID: 7, RequestID: 2, BlockedBy: 9, ElapsedMs: 1000, SQL: "UPDATE orders SET status = ?", Database: "orders", Engine: EnginePostgres}, {SessionID: 9, RequestID: 3}})
		Expect(rows).To(HaveLen(2))
		Expect(rows[0]).To(Equal(ActiveSample{Source: "connection://orders", OperationID: "7/2", BlockedByOperationID: "9/3", SampledAt: serverTime, StartedAt: serverTime.Add(-time.Second), ElapsedMs: 1000, API: "database", Database: "orders", Engine: EnginePostgres, SQL: "UPDATE orders SET status = ?", Connection: "connection://orders"}))
	})

	It("does not serialize unobserved database metrics into JVM operations", func() {
		data, err := json.Marshal(ActiveSample{Source: "java/1", OperationID: "one", SampledAt: serverTime, StartedAt: serverTime.Add(-time.Second), ElapsedMs: 1000, API: "jdbc", SQLUnavailableReason: "prepared-before-instrumentation"})
		Expect(err).NotTo(HaveOccurred())
		var row map[string]any
		Expect(json.Unmarshal(data, &row)).To(Succeed())
		Expect(row).NotTo(HaveKey("sessionId"))
		Expect(row).NotTo(HaveKey("blocked"))
		Expect(row).NotTo(HaveKey("cpuMs"))
		Expect(row).NotTo(HaveKey("openTransactions"))
	})

	It("allows JVM agent targets without database credentials and rejects database-only settings", func() {
		params := Params{Target: target.Target{Seam: target.Agent}}
		Expect(params.Validate()).To(Succeed())
		params.Idle = true
		Expect(params.Validate()).To(MatchError(ContainSubstring("idle")))
		params.Idle = false
		params.Connection = "connection://orders"
		Expect(params.Validate()).To(MatchError(ContainSubstring("connection")))
	})
})
