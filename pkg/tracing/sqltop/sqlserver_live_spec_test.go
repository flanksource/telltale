package sqltop

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/models"
	"github.com/flanksource/telltale/pkg/tracing/sqlconn"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func openSession(ctx context.Context, pool *sql.DB) backend {
	conn, err := pool.Conn(ctx)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(conn.Close)
	var spid int64
	Expect(conn.QueryRowContext(ctx, "SELECT CAST(@@SPID AS bigint)").Scan(&spid)).To(Succeed())
	return backend{conn: conn, pid: spid}
}

var _ = Describe("sql top on a live SQL Server", func() {
	It("records a waiting batch, a lock waiter with its blocker, and the sleeping blocker holding its transaction", func(spec SpecContext) {
		address := strings.TrimSpace(os.Getenv("COMMONS_DB_SQLSERVER_URL"))
		if address == "" {
			Skip("COMMONS_DB_SQLSERVER_URL is not set; this fixture creates and drops one table")
		}
		ctx := dbcontext.New().WithConnectionResolver(func(dbcontext.Context, string) (*models.Connection, error) {
			return &models.Connection{Name: "live", Type: models.ConnectionTypeSQLServer, URL: address}, nil
		})
		pool, _, release, err := sqlconn.Open(ctx, sqlconn.OpenOptions{Reference: "connection://live", Types: []string{models.ConnectionTypeSQLServer}, Purpose: "the live spec"})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(release)
		table := fmt.Sprintf("dbo.telltale_sqltop_%d", time.Now().UnixNano())
		_, err = pool.ExecContext(spec, "CREATE TABLE "+table+" (id int PRIMARY KEY, value int); INSERT INTO "+table+" VALUES (1, 0)")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _, dropErr := pool.Exec("DROP TABLE " + table); Expect(dropErr).NotTo(HaveOccurred()) })

		holder, waiter, sleeper := openSession(spec, pool), openSession(spec, pool), openSession(spec, pool)
		_, err = holder.conn.ExecContext(spec, "BEGIN TRAN; UPDATE "+table+" SET value = value + 1 WHERE id = 1")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _, _ = holder.conn.ExecContext(context.Background(), "IF @@TRANCOUNT > 0 ROLLBACK") })
		background, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		go func() { _, _ = waiter.conn.ExecContext(background, "UPDATE "+table+" SET value = value + 1 WHERE id = 1") }()
		go func() { _, _ = sleeper.conn.ExecContext(background, "WAITFOR DELAY '00:00:30'") }()
		Eventually(func() int {
			var waiting int
			Expect(pool.QueryRowContext(spec, "SELECT COUNT(*) FROM sys.dm_exec_requests WHERE session_id IN (@p1, @p2) AND wait_type IN ('LCK_M_U', 'LCK_M_X', 'WAITFOR')",
				waiter.pid, sleeper.pid).Scan(&waiting)).To(Succeed())
			return waiting
		}).WithTimeout(10 * time.Second).Should(Equal(2))

		out := &recorder{}
		_, stop, err := handler{}.Prepare(ctx, Params{Options: Options{Connection: "connection://live"}}, out)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(stop)

		for _, row := range out.rows {
			Expect(row.SQL).NotTo(ContainSubstring(pollMarker), "the poller recorded its own session")
			Expect(row.Engine).To(Equal(EngineSQLServer))
		}
		sleeping := sampleOf(out.rows, sleeper.pid)
		Expect(sleeping.WaitType).To(Equal("WAITFOR"))
		Expect(sleeping.SQL).To(ContainSubstring("WAITFOR DELAY"))
		Expect(sleeping.ElapsedMs).To(BeNumerically(">", 0))
		blocked := sampleOf(out.rows, waiter.pid)
		Expect(blocked.Blocked).To(BeTrue())
		Expect(blocked.BlockedBy).To(Equal(holder.pid))
		Expect(blocked.WaitType).To(HavePrefix("LCK_M_"))
		Expect(blocked.WaitResource).NotTo(BeEmpty())
		Expect(blocked.QueryID).To(HavePrefix("0x"))
		blocker := sampleOf(out.rows, holder.pid)
		Expect(blocker.State).To(Equal("sleeping"))
		Expect(blocker.OpenTransactions).To(BeNumerically(">=", 1))
		Expect(blocker.TransactionMs).To(BeNumerically(">", 0))
	})
})
