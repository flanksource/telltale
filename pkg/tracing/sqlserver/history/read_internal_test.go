package history

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/sqlserver"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// renderRead renders the read through the real SQL Server dialector without a
// connection, the way GORM would send it.
func renderRead(options ReadOptions) string {
	GinkgoHelper()
	db, err := gorm.Open(sqlserver.Open(""), &gorm.Config{
		DryRun: true, Logger: logger.Default.LogMode(logger.Silent), DisableAutomaticPing: true,
	})
	Expect(err).NotTo(HaveOccurred())
	query, args := readQuery(Source{FilePattern: "/var/opt/mssql/log/system_health*.xel"}, options)
	return db.ToSQL(func(tx *gorm.DB) *gorm.DB {
		var rows []struct{ EventData string }
		return tx.Raw(query, args...).Find(&rows)
	})
}

var _ = Describe("readQuery", func() {
	window := Window{
		From: time.Date(2026, 9, 10, 16, 0, 0, 0, time.UTC),
		To:   time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC),
	}

	It("bounds the read by the file pattern and the UTC window, scoped to the connected database by default", func() {
		sql := renderRead(ReadOptions{Window: window})

		Expect(sql).To(ContainSubstring(`sys.fn_xe_file_target_read_file('/var/opt/mssql/log/system_health*.xel', NULL, NULL, NULL)`))
		Expect(sql).To(ContainSubstring(`f.object_name = 'xml_deadlock_report'`))
		Expect(sql).To(ContainSubstring(`>= CONVERT(datetime2, '2026-09-10T16:00:00.0000000', 126)`))
		Expect(sql).To(ContainSubstring(`< CONVERT(datetime2, '2026-09-10T18:00:00.0000000', 126)`))
		Expect(sql).To(ContainSubstring(`value('@currentdbname', 'nvarchar(128)') = DB_NAME()`))
		Expect(sql).To(ContainSubstring(`data[@name="xml_report"]`), "the XQuery's @name survives GORM's placeholder rewrite")
		Expect(sql).NotTo(ContainSubstring("ORDER BY"))
		Expect(sql).NotTo(HavePrefix("WITH"))
	})

	It("pushes literal database names into an IN list", func() {
		sql := renderRead(ReadOptions{Window: window, Databases: DatabaseScope{Names: []string{"tenant_a", "tenant_b"}}})

		Expect(sql).To(ContainSubstring(`value('@currentdbname', 'nvarchar(128)') IN ('tenant_a','tenant_b')`))
	})

	It("reads every database when the scope is the whole instance", func() {
		sql := renderRead(ReadOptions{Window: window, Databases: DatabaseScope{All: true}})

		Expect(sql).NotTo(ContainSubstring("currentdbname"))
	})

	It("reads from the oldest graph the files retain when the window has no start", func() {
		query, args := readQuery(Source{FilePattern: "/var/opt/mssql/log/system_health*.xel"}, ReadOptions{
			Window: Window{To: window.To}, Databases: DatabaseScope{All: true},
		})

		Expect(query).NotTo(ContainSubstring(">="))
		Expect(query).To(ContainSubstring("< CONVERT(datetime2, ?, 126)"))
		Expect(args).To(Equal([]any{"/var/opt/mssql/log/system_health*.xel", "2026-09-10T18:00:00.0000000"}))
	})

	It("converts a window given in another zone to UTC before binding it", func() {
		johannesburg := time.FixedZone("SAST", 2*60*60)
		query, args := readQuery(Source{}, ReadOptions{Window: Window{
			From: time.Date(2026, 9, 10, 18, 0, 0, 0, johannesburg),
			To:   time.Date(2026, 9, 10, 20, 0, 0, 0, johannesburg),
		}})

		Expect(query).NotTo(BeEmpty())
		Expect(args[1:3]).To(Equal([]any{"2026-09-10T16:00:00.0000000", "2026-09-10T18:00:00.0000000"}))
	})
})
