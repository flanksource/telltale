package plancache

import (
	"errors"
	"io"
	"log"
	"os"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm/logger"
)

var _ = Describe("plan cache database logging", func() {
	It("writes neither slow queries nor SQL errors to stdout", func() {
		stdout, defaultLogger := os.Stdout, logger.Default
		read, write, err := os.Pipe()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(read.Close)
		DeferCleanup(func() {
			os.Stdout, logger.Default = stdout, defaultLogger
			if write != nil {
				Expect(write.Close()).To(Succeed())
			}
		})
		os.Stdout = write
		logger.Default = logger.New(log.New(os.Stdout, "", 0), logger.Config{LogLevel: logger.Warn, SlowThreshold: time.Nanosecond})
		pool, mock, err := sqlmock.New()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			mock.ExpectClose()
			Expect(pool.Close()).To(Succeed())
		})
		db, err := openDB(pool)
		Expect(err).NotTo(HaveOccurred())
		mock.ExpectExec("SELECT 1").WillReturnResult(sqlmock.NewResult(0, 0))
		Expect(db.Exec("SELECT 1").Error).NotTo(HaveOccurred())
		failure := errors.New("SQL query failed")
		mock.ExpectExec("SELECT 2").WillReturnError(failure)
		Expect(db.Exec("SELECT 2").Error).To(MatchError(failure))
		Expect(mock.ExpectationsWereMet()).To(Succeed())
		Expect(write.Close()).To(Succeed())
		write = nil
		output, err := io.ReadAll(read)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(output)).To(BeEmpty())
	})
})
