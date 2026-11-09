package sqltop

import (
	"context"
	"errors"
	"time"

	"github.com/flanksource/commons-db/query"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var serverTime = time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)

// recorder is an Emitter that keeps what a capture emits and reports.
type recorder struct {
	rows   []Sample
	report any
}

func (r *recorder) Emit(_ context.Context, row Sample) error { r.rows = append(r.rows, row); return nil }
func (r *recorder) TryEmit(row Sample) bool                  { r.rows = append(r.rows, row); return true }
func (r *recorder) Report(summary any)                       { r.report = summary }
func (r *recorder) Warn(string)                              {}
func (r *recorder) Describe(string, ...query.SessionMetadata) {}
func (r *recorder) Export(string, string)                    {}

func started(ago time.Duration) time.Time { return serverTime.Add(-ago) }

func pollOnce(options Options, result snapshot) (*recorder, error) {
	var idleAsked bool
	c, err := newCapture(options, EnginePostgres, func(_ context.Context, idle bool) (snapshot, error) {
		idleAsked = idle
		return result, nil
	})
	Expect(err).NotTo(HaveOccurred())
	out := &recorder{}
	err = c.poll(context.Background(), out)
	Expect(idleAsked).To(Equal(options.Idle))
	return out, err
}

func sessions(rows []Sample) []int64 {
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.SessionID)
	}
	return ids
}

var _ = Describe("sql top options", func() {
	DescribeTable("refuses invalid options before connecting", func(options Options, message string) {
		Expect(options.Validate()).To(MatchError(ContainSubstring(message)))
	},
		Entry("no connection", Options{}, "connection is required"),
		Entry("unparsable poll", Options{Connection: "connection://db", Poll: "soon"}, `poll "soon"`),
		Entry("zero poll", Options{Connection: "connection://db", Poll: "0s"}, "positive duration"),
		Entry("unparsable minimum elapsed", Options{Connection: "connection://db", MinElapsed: "long"}, `minElapsed "long"`),
		Entry("negative top", Options{Connection: "connection://db", Top: -1}, "top must be non-negative"),
		Entry("bare database exclusion", Options{Connection: "connection://db", Databases: []string{"orders,!"}}, "databases pattern"),
		Entry("bare text exclusion", Options{Connection: "connection://db", Text: []string{"!"}}, "text pattern"),
	)
	It("defaults to a five second poll and the fifty longest requests", func() {
		options := (handler{}).Params().Options
		Expect(options.Validate()).To(MatchError(ContainSubstring("connection is required")))
		options.Connection = "connection://db"
		Expect(options.Validate()).To(Succeed())
		Expect(options.pollInterval()).To(Equal(5 * time.Second))
		Expect(Options{}.top()).To(Equal(50))
	})
})

var _ = Describe("sql top capture", func() {
	It("stamps every request with the server time and derives elapsed, transaction age and blocked", func() {
		out, err := pollOnce(Options{}, snapshot{ServerTime: serverTime, Rows: []Sample{
			{SessionID: 7, StartedAt: started(1500 * time.Millisecond), TransactionStartedAt: started(4 * time.Second), BlockedBy: 9},
			{SessionID: 9, ElapsedMs: 250, StartedAt: started(time.Hour)},
		}})
		Expect(err).NotTo(HaveOccurred())
		Expect(out.rows).To(Equal([]Sample{
			{SampledAt: serverTime, Engine: EnginePostgres, SessionID: 7, BlockedBy: 9, Blocked: true,
				StartedAt: started(1500 * time.Millisecond), TransactionStartedAt: started(4 * time.Second), ElapsedMs: 1500, TransactionMs: 4000},
			{SampledAt: serverTime, Engine: EnginePostgres, SessionID: 9, ElapsedMs: 250, StartedAt: started(time.Hour)},
		}))
		Expect(out.report).To(Equal(map[string]any{"engine": EnginePostgres, "polls": 1, "active": 2, "blocked": 1, "recorded": 2}))
	})
	It("ranks by elapsed time, longest first, and keeps the top requests", func() {
		out, err := pollOnce(Options{Top: 2}, snapshot{ServerTime: serverTime, Rows: []Sample{
			{SessionID: 1, ElapsedMs: 10}, {SessionID: 2, ElapsedMs: 300}, {SessionID: 3, ElapsedMs: 20},
		}})
		Expect(err).NotTo(HaveOccurred())
		Expect(sessions(out.rows)).To(Equal([]int64{2, 3}))
		Expect(out.report).To(HaveKeyWithValue("active", 3))
		Expect(out.report).To(HaveKeyWithValue("recorded", 2))
	})
	It("drops requests shorter than the minimum elapsed time", func() {
		out, err := pollOnce(Options{MinElapsed: "100ms"}, snapshot{ServerTime: serverTime, Rows: []Sample{
			{SessionID: 1, ElapsedMs: 99}, {SessionID: 2, ElapsedMs: 100},
		}})
		Expect(err).NotTo(HaveOccurred())
		Expect(sessions(out.rows)).To(Equal([]int64{2}))
	})
	DescribeTable("filters by database and statement text patterns", func(options Options, want []int64) {
		out, err := pollOnce(options, snapshot{ServerTime: serverTime, Rows: []Sample{
			{SessionID: 1, ElapsedMs: 3, Database: "orders", SQL: "SELECT * FROM ledger"},
			{SessionID: 2, ElapsedMs: 2, Database: "orders_archive", SQL: "UPDATE secrets SET value = 1"},
			{SessionID: 3, ElapsedMs: 1, Database: "billing", SQL: "select pg_sleep(5)"},
		}})
		Expect(err).NotTo(HaveOccurred())
		Expect(sessions(out.rows)).To(Equal(want))
	},
		Entry("every database when unset", Options{}, []int64{1, 2, 3}),
		Entry("a wildcard database", Options{Databases: []string{"orders*"}}, []int64{1, 2}),
		Entry("an excluded database", Options{Databases: []string{"!billing"}}, []int64{1, 2}),
		Entry("a bare text value matches a substring, ignoring case", Options{Text: []string{"PG_SLEEP"}}, []int64{3}),
		Entry("an excluded text value", Options{Text: []string{"!secrets"}}, []int64{1, 3}),
		Entry("database and text together", Options{Databases: []string{"orders*"}, Text: []string{"ledger"}}, []int64{1}),
	)
	It("asks the engine for idle sessions only when the options do", func() {
		_, err := pollOnce(Options{Idle: true}, snapshot{ServerTime: serverTime})
		Expect(err).NotTo(HaveOccurred())
	})
	It("records an idle server as a poll with no rows", func() {
		out, err := pollOnce(Options{}, snapshot{ServerTime: serverTime})
		Expect(err).NotTo(HaveOccurred())
		Expect(out.rows).To(BeEmpty())
		Expect(out.report).To(HaveKeyWithValue("polls", 1))
	})
	DescribeTable("refuses a snapshot it cannot trust", func(result snapshot, message string) {
		out, err := pollOnce(Options{}, result)
		Expect(err).To(MatchError(ContainSubstring(message)))
		Expect(out.rows).To(BeEmpty())
	},
		Entry("no server time", snapshot{Rows: []Sample{{SessionID: 1}}}, "no server timestamp"),
		Entry("one request twice", snapshot{ServerTime: serverTime, Rows: []Sample{{SessionID: 4, RequestID: 1}, {SessionID: 4, RequestID: 1}}}, "session 4 request 1 twice"),
	)
	It("keeps two requests of one session apart", func() {
		out, err := pollOnce(Options{}, snapshot{ServerTime: serverTime, Rows: []Sample{{SessionID: 4, RequestID: 0}, {SessionID: 4, RequestID: 1}}})
		Expect(err).NotTo(HaveOccurred())
		Expect(out.rows).To(HaveLen(2))
	})
	It("surfaces a failed read with the engine it read", func() {
		c, err := newCapture(Options{}, EngineSQLServer, func(context.Context, bool) (snapshot, error) {
			return snapshot{}, errors.New("login failed")
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(c.poll(context.Background(), &recorder{})).To(MatchError("sample sqlserver requests: login failed"))
	})
	DescribeTable("names a statement's command by its first keyword", func(statement, command string) {
		Expect(commandOf(statement)).To(Equal(command))
	},
		Entry("a select", "  select 1", "SELECT"),
		Entry("a call with parentheses", "vacuum(analyze) orders", "VACUUM"),
		Entry("no statement", "   ", ""),
	)
})
