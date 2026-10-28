package plancache

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("incremental plan cache snapshots", func() {
	It("accepts an empty active result set while retaining the membership and server timestamp", func() {
		pool, mock, err := sqlmock.New()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { mock.ExpectClose(); Expect(pool.Close()).To(Succeed()) })
		db, err := openDB(pool)
		Expect(err).NotTo(HaveOccurred())
		mock.ExpectQuery(".").WillReturnRows(
			sqlmock.NewRows([]string{"sampled_at"}).AddRow(planCreated),
			sqlmock.NewRows([]string{"plan_handle"}).AddRow([]byte{1}),
			sqlmock.NewRows([]string{"plan_handle", "execution_count"}),
		)
		got, err := readSnapshot(context.Background(), db, snapshotOptions{Since: planCreated})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(cacheSnapshot{ServerTime: planCreated, Handles: []string{"0x01"}}))
		Expect(mock.ExpectationsWereMet()).To(Succeed())
	})
	DescribeTable("rejects incomplete and failed snapshot result sets", func(resultSets func() []*sqlmock.Rows, message string) {
		pool, mock, err := sqlmock.New()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { mock.ExpectClose(); Expect(pool.Close()).To(Succeed()) })
		db, err := openDB(pool)
		Expect(err).NotTo(HaveOccurred())
		mock.ExpectQuery(".").WillReturnRows(resultSets()...)
		_, err = readSnapshot(context.Background(), db, snapshotOptions{})
		Expect(err).To(MatchError(ContainSubstring(message)))
		Expect(mock.ExpectationsWereMet()).To(Succeed())
	},
		Entry("missing timestamp", func() []*sqlmock.Rows { return []*sqlmock.Rows{sqlmock.NewRows([]string{"sampled_at"})} }, "server timestamp"),
		Entry("missing statistics", func() []*sqlmock.Rows {
			return []*sqlmock.Rows{
				sqlmock.NewRows([]string{"sampled_at"}).AddRow(planCreated), sqlmock.NewRows([]string{"plan_handle"}),
			}
		}, "active plans result set"),
		Entry("membership read error", func() []*sqlmock.Rows {
			return []*sqlmock.Rows{
				sqlmock.NewRows([]string{"sampled_at"}).AddRow(planCreated),
				sqlmock.NewRows([]string{"plan_handle"}).AddRow([]byte{1}).RowError(0, errors.New("membership interrupted")),
			}
		}, "membership interrupted"),
		Entry("statistics read error", func() []*sqlmock.Rows {
			return []*sqlmock.Rows{
				sqlmock.NewRows([]string{"sampled_at"}).AddRow(planCreated), sqlmock.NewRows([]string{"plan_handle"}),
				sqlmock.NewRows([]string{"plan_handle"}).AddRow([]byte{1}).RowError(0, errors.New("statistics interrupted")),
			}
		}, "statistics interrupted"),
	)
	It("filters at plan grain after baseline without filtering statement counters before aggregation", func() {
		q, err := snapshotQuery(dryDB(), snapshotOptions{Since: planCreated})
		Expect(err).NotTo(HaveOccurred())
		var rows []cachedPlanRow
		tx := q.Select(selectList).Find(&rows)
		Expect(tx.Statement.SQL.String()).To(ContainSubstring("qs.last_execution_time >="))
		Expect(tx.Statement.Vars).To(ContainElement(planCreated))
		Expect(tx.Statement.SQL.String()).To(ContainSubstring("SUM(execution_count)"))
		Expect(tx.Statement.SQL.String()).NotTo(ContainSubstring("WHERE last_execution_time"))
		q, err = snapshotQuery(dryDB(), snapshotOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(q.Select(selectList).Find(&rows).Statement.SQL.String()).NotTo(ContainSubstring("qs.last_execution_time >="))
	})
	It("reads server time, handle-only membership and active plans in one marked SQL batch", func() {
		var statement string
		pool, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherFunc(func(_, actual string) error { statement = actual; return nil })))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { mock.ExpectClose(); Expect(pool.Close()).To(Succeed()) })
		db, err := openDB(pool)
		Expect(err).NotTo(HaveOccurred())
		mock.ExpectQuery("snapshot").WithArgs(pollMarker, "Compiled Plan", planCreated).WillReturnRows(
			sqlmock.NewRows([]string{"sampled_at"}).AddRow(planCreated.Add(time.Second)),
			sqlmock.NewRows([]string{"plan_handle"}).AddRow([]byte{1}).AddRow([]byte{2}),
			sqlmock.NewRows([]string{"plan_handle", "execution_count", "total_worker_time", "creation_time"}).AddRow([]byte{1}, 12, 25000, planCreated),
		)
		got, err := readSnapshot(context.Background(), db, snapshotOptions{Since: planCreated})
		Expect(err).NotTo(HaveOccurred())
		Expect(got.ServerTime).To(Equal(planCreated.Add(time.Second)))
		Expect(got.Handles).To(ConsistOf("0x01", "0x02"))
		Expect(got.Plans).To(HaveLen(1))
		Expect(got.Plans[0].TotalCPU).To(Equal(25 * time.Millisecond))
		Expect(strings.TrimSpace(statement)).To(HavePrefix(pollMarker))
		Expect(statement).To(ContainSubstring("SYSDATETIME()"))
		Expect(statement).To(ContainSubstring("SELECT cp.plan_handle FROM sys.dm_exec_cached_plans AS cp;"))
		Expect(statement).To(ContainSubstring("qs.last_execution_time >="))
		Expect(statement).To(ContainSubstring("CHARINDEX("))
		Expect(mock.ExpectationsWereMet()).To(Succeed())
	})
	It("fails if the batch omits membership rather than treating every plan as evicted", func() {
		pool, mock, err := sqlmock.New()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { mock.ExpectClose(); Expect(pool.Close()).To(Succeed()) })
		db, err := openDB(pool)
		Expect(err).NotTo(HaveOccurred())
		mock.ExpectQuery(".").WillReturnRows(sqlmock.NewRows([]string{"sampled_at"}).AddRow(planCreated))
		_, err = readSnapshot(context.Background(), db, snapshotOptions{})
		Expect(err).To(MatchError(ContainSubstring("membership")))
		Expect(mock.ExpectationsWereMet()).To(Succeed())
	})
	It("surfaces SQL errors without advancing the snapshot", func() {
		pool, mock, err := sqlmock.New()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { mock.ExpectClose(); Expect(pool.Close()).To(Succeed()) })
		db, err := openDB(pool)
		Expect(err).NotTo(HaveOccurred())
		failure := errors.New("SQL Server unavailable")
		mock.ExpectQuery(".").WillReturnError(failure)
		_, err = readSnapshot(context.Background(), db, snapshotOptions{})
		Expect(err).To(MatchError(ContainSubstring(failure.Error())))
		Expect(mock.ExpectationsWereMet()).To(Succeed())
	})
})
