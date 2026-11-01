package deadlock

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/flanksource/clicky"
	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/models"
	_ "github.com/flanksource/commons-db/query/providers"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/sqlserver/xeprovider"
	"github.com/flanksource/telltale/pkg/tracing/tracingtest"
	mssql "github.com/microsoft/go-mssqldb"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/sqlserver"
	"gorm.io/gorm"
)

var _ = Describe("SQL deadlocks (live)", func() {
	It("reads a generated deadlock in follow and historical windows without changing system_health", func() {
		address := strings.TrimSpace(os.Getenv("COMMONS_DB_SQLSERVER_URL"))
		if address == "" {
			Skip("COMMONS_DB_SQLSERVER_URL is not set; this fixture creates and drops one table")
		}
		ctx := dbcontext.New().WithConnectionResolver(func(dbcontext.Context, string) (*models.Connection, error) {
			return &models.Connection{Name: "live", Type: models.ConnectionTypeSQLServer, URL: address}, nil
		})
		pool, release, err := xeprovider.OpenSQLServer(ctx, "connection://live")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(release)
		db, err := gorm.Open(sqlserver.New(sqlserver.Config{Conn: pool}), &gorm.Config{DisableAutomaticPing: true})
		Expect(err).NotTo(HaveOccurred())
		span, err := BeginDeadlockSpan(ctx, GormDeadlockReader(db), clicky.MultiFilter{}, Window{}, nil)
		Expect(err).NotTo(HaveOccurred())
		table := fmt.Sprintf("dbo.telltale_deadlock_%d", time.Now().UnixNano())
		_, err = pool.ExecContext(ctx, "CREATE TABLE "+table+" (id int PRIMARY KEY, value int); INSERT INTO "+table+" VALUES (1, 0), (2, 0)")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _, dropErr := pool.Exec("DROP TABLE " + table); Expect(dropErr).NotTo(HaveOccurred()) })
		first, err := pool.BeginTx(ctx, nil)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = first.Rollback() }()
		second, err := pool.BeginTx(ctx, nil)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = second.Rollback() }()
		_, err = first.ExecContext(ctx, "UPDATE "+table+" SET value=value+1 WHERE id=1")
		Expect(err).NotTo(HaveOccurred())
		_, err = second.ExecContext(ctx, "UPDATE "+table+" SET value=value+1 WHERE id=2")
		Expect(err).NotTo(HaveOccurred())
		results := make(chan error, 2)
		update := func(tx *sql.Tx, id int) {
			_, updateErr := tx.ExecContext(ctx, fmt.Sprintf("UPDATE %s SET value=value+1 WHERE id=%d", table, id))
			_ = tx.Rollback()
			results <- updateErr
		}
		go update(first, 2)
		go update(second, 1)
		victims := 0
		for range 2 {
			select {
			case updateErr := <-results:
				if updateErr != nil {
					var serverErr mssql.Error
					Expect(errors.As(updateErr, &serverErr)).To(BeTrue(), updateErr)
					Expect(serverErr.Number).To(Equal(int32(1205)))
					victims++
				}
			case <-time.After(20 * time.Second):
				Fail("fixture transactions did not resolve their deadlock")
			}
		}
		Expect(victims).To(Equal(1))
		time.Sleep(time.Second)
		finalCtx, cancel := context.WithTimeout(ctx, 4*time.Minute)
		defer cancel()
		follow, err := span.Final(finalCtx)
		Expect(err).NotTo(HaveOccurred())
		Expect(follow.Result.ServerDeadlocks).To(BeNumerically(">=", 1))
		Expect(follow.Rows).NotTo(BeEmpty(), follow.Result)
		Expect(follow.Rows[0].XML).To(ContainSubstring("<deadlock>"))
		Expect(follow.Rows[0].Victims).To(Equal(1))
		Expect(follow.Rows[0].Participants).To(Equal(2))
		env := tracingtest.NewEnv(map[string]tracing.TracePlugin{"sql_deadlock": Kind()})
		params, err := json.Marshal(Params{Options: Options{Connection: "connection://live", From: follow.Result.From.Format(time.RFC3339Nano), To: follow.Result.To.Format(time.RFC3339Nano)}})
		Expect(err).NotTo(HaveOccurred())
		managed, err := env.Runtime.Start(ctx, tracing.StartRequest{Kind: "sql_deadlock", Params: params})
		Expect(err).NotTo(HaveOccurred())
		info := tracingtest.Ended(managed.Session())
		Expect(info.Error).To(BeEmpty())
		Expect(env.Sealed(info)).To(BeTrue())
		Expect(env.Rows(info)).To(ContainElement(HaveKeyWithValue("id", follow.Rows[0].ID)))
		var healthy int
		Expect(pool.QueryRow("SELECT COUNT(*) FROM sys.dm_xe_sessions WHERE name='system_health'").Scan(&healthy)).To(Succeed())
		Expect(healthy).To(Equal(1))
	})
})
