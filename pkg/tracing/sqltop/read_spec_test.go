package sqltop

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var postgresColumns = []string{"pid", "blocked_by", "datname", "usename", "application_name", "client_host", "state",
	"wait_event_type", "wait_event", "query_start", "xact_start", "query_id", "query"}

var sqlServerColumns = []string{"session_id", "request_id", "blocked_by", "database_name", "login_name", "program_name", "host_name",
	"state", "command", "wait_type", "wait_resource", "wait_time", "started_at", "transaction_started_at", "total_elapsed_time",
	"cpu_time", "logical_reads", "reads", "writes", "memory_bytes", "open_transaction_count", "query_hash", "sql_text"}

func mockDB(matcher sqlmock.QueryMatcher) (*sql.DB, sqlmock.Sqlmock) {
	pool, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(matcher))
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { mock.ExpectClose(); Expect(pool.Close()).To(Succeed()) })
	return pool, mock
}

var _ = Describe("reading Postgres activity", func() {
	It("reads the server time and the activity view in one read-only transaction", func() {
		pool, mock := mockDB(sqlmock.QueryMatcherRegexp)
		mock.ExpectBegin()
		mock.ExpectQuery(`telltale:sql_top \*/ SELECT now\(\)`).WillReturnRows(sqlmock.NewRows([]string{"now"}).AddRow(serverTime))
		mock.ExpectQuery(`FROM pg_stat_activity`).WithArgs(true).WillReturnRows(sqlmock.NewRows(postgresColumns).
			AddRow(41, 40, "orders", "app", "checkout", "10.0.0.5", "active", "Lock", "transactionid",
				started(2*time.Second), started(3*time.Second), "-7230", "UPDATE orders SET paid = true").
			AddRow(40, 0, "orders", "app", "checkout", "", "idle in transaction", "", "", started(time.Minute), started(time.Minute), "", ""))
		mock.ExpectRollback()
		got, err := readPostgres(context.Background(), pool, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(snapshot{ServerTime: serverTime, Rows: []Sample{
			{SessionID: 41, BlockedBy: 40, Database: "orders", User: "app", Application: "checkout", ClientHost: "10.0.0.5",
				State: "active", WaitType: "Lock:transactionid", StartedAt: started(2 * time.Second), TransactionStartedAt: started(3 * time.Second),
				OpenTransactions: 1, QueryID: "-7230", Command: "UPDATE", SQL: "UPDATE orders SET paid = true"},
			{SessionID: 40, Database: "orders", User: "app", Application: "checkout", State: "idle in transaction",
				StartedAt: started(time.Minute), TransactionStartedAt: started(time.Minute), OpenTransactions: 1},
		}}))
		Expect(mock.ExpectationsWereMet()).To(Succeed())
	})
	It("excludes the poller's own backend and, unless asked, idle sessions", func() {
		Expect(postgresActivity).To(ContainSubstring("a.pid <> pg_backend_pid()"))
		Expect(postgresActivity).To(ContainSubstring("a.state IS DISTINCT FROM 'idle' OR $1"))
		Expect(postgresActivity).To(ContainSubstring("a.backend_type = 'client backend'"))
	})
	It("surfaces a failed activity read", func() {
		pool, mock := mockDB(sqlmock.QueryMatcherRegexp)
		mock.ExpectBegin()
		mock.ExpectQuery(`SELECT now\(\)`).WillReturnRows(sqlmock.NewRows([]string{"now"}).AddRow(serverTime))
		mock.ExpectQuery(`FROM pg_stat_activity`).WillReturnError(sql.ErrConnDone)
		mock.ExpectRollback()
		_, err := readPostgres(context.Background(), pool, false)
		Expect(err).To(MatchError(ContainSubstring("read pg_stat_activity")))
		Expect(mock.ExpectationsWereMet()).To(Succeed())
	})
})

var _ = Describe("reading SQL Server requests", func() {
	It("reads the server time and every request in one marked batch with the idle switch named", func() {
		var statement string
		pool, mock := mockDB(sqlmock.QueryMatcherFunc(func(_, actual string) error { statement = actual; return nil }))
		mock.ExpectQuery("batch").WithArgs(sql.Named("idle", false)).WillReturnRows(
			sqlmock.NewRows([]string{"sampled_at"}).AddRow(serverTime),
			sqlmock.NewRows(sqlServerColumns).
				AddRow(55, 0, 54, "warehouse", "etl", "loader", "host-a", "suspended", "UPDATE", "LCK_M_X", "KEY: 5:72057594043236352 (8194443284a0)",
					1200, started(1300*time.Millisecond), started(5*time.Second), 1300, 40, 900, 12, 3, 16384, 1, []byte{0xab, 0x01}, "UPDATE dbo.ledger SET v = 1").
				AddRow(54, 0, 0, "warehouse", "etl", "loader", "host-a", "sleeping", "", "", "", 0, started(time.Minute), started(time.Minute), nil, 0, 0, 0, 0, 0, 1, nil, "BEGIN TRAN"),
		)
		got, err := readSQLServer(context.Background(), pool, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(snapshot{ServerTime: serverTime, Rows: []Sample{
			{SessionID: 55, BlockedBy: 54, Database: "warehouse", User: "etl", Application: "loader", ClientHost: "host-a",
				State: "suspended", Command: "UPDATE", WaitType: "LCK_M_X", WaitResource: "KEY: 5:72057594043236352 (8194443284a0)", WaitMs: 1200,
				StartedAt: started(1300 * time.Millisecond), TransactionStartedAt: started(5 * time.Second), ElapsedMs: 1300, CPUMs: 40,
				LogicalReads: 900, Reads: 12, Writes: 3, MemoryBytes: 16384, OpenTransactions: 1, QueryID: "0xAB01", SQL: "UPDATE dbo.ledger SET v = 1"},
			{SessionID: 54, Database: "warehouse", User: "etl", Application: "loader", ClientHost: "host-a", State: "sleeping",
				StartedAt: started(time.Minute), TransactionStartedAt: started(time.Minute), OpenTransactions: 1, SQL: "BEGIN TRAN"},
		}}))
		Expect(strings.TrimSpace(statement)).To(HavePrefix(pollMarker))
		Expect(statement).To(ContainSubstring("s.session_id <> @@SPID"))
		Expect(statement).To(ContainSubstring("s.open_transaction_count > 0 OR @idle = 1"))
		Expect(statement).To(ContainSubstring("statement_start_offset"))
		Expect(mock.ExpectationsWereMet()).To(Succeed())
	})
	DescribeTable("refuses an incomplete batch", func(resultSets func() []*sqlmock.Rows, message string) {
		pool, mock := mockDB(sqlmock.QueryMatcherRegexp)
		mock.ExpectQuery(".").WillReturnRows(resultSets()...)
		_, err := readSQLServer(context.Background(), pool, false)
		Expect(err).To(MatchError(ContainSubstring(message)))
		Expect(mock.ExpectationsWereMet()).To(Succeed())
	},
		Entry("no server time", func() []*sqlmock.Rows { return []*sqlmock.Rows{sqlmock.NewRows([]string{"sampled_at"})} }, "no server timestamp"),
		Entry("no requests", func() []*sqlmock.Rows {
			return []*sqlmock.Rows{sqlmock.NewRows([]string{"sampled_at"}).AddRow(serverTime)}
		}, "no requests result set"),
		Entry("two server times", func() []*sqlmock.Rows {
			return []*sqlmock.Rows{sqlmock.NewRows([]string{"sampled_at"}).AddRow(serverTime).AddRow(serverTime), sqlmock.NewRows(sqlServerColumns)}
		}, "multiple server timestamps"),
	)
})
