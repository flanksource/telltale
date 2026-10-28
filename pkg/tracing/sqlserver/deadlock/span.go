package deadlock

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/flanksource/clicky"
	deadlockgraph "github.com/flanksource/telltale/pkg/tracing/sqlserver/deadlocks"
	deadlocks "github.com/flanksource/telltale/pkg/tracing/sqlserver/history"
	"gorm.io/gorm"
)

// sqlDeadlockDispatchMargin is added to system_health's dispatch latency
// before a span that saw deadlocks reads their graphs, so a graph dispatched
// right at the latency is on disk by the time it is read.
const sqlDeadlockDispatchMargin = 2 * time.Second

// DeadlockReader is the slice of SQL Server a sql_deadlock span reads.
type DeadlockReader interface {
	ServerNow(ctx context.Context) (time.Time, error)
	DeadlockCounter(ctx context.Context) (int64, error)
	// DispatchLatency is how long system_health can hold an event before it
	// reaches the files LoadWindow reads.
	DispatchLatency(ctx context.Context) (time.Duration, error)
	LoadWindow(ctx context.Context, options deadlocks.ReadOptions) (deadlocks.Loaded, error)
}

type gormDeadlockReader struct{ db *gorm.DB }

// GormDeadlockReader reads deadlocks through db.
func GormDeadlockReader(db *gorm.DB) DeadlockReader { return gormDeadlockReader{db: db} }

func (r gormDeadlockReader) ServerNow(ctx context.Context) (time.Time, error) {
	return deadlocks.ServerNow(ctx, r.db)
}

func (r gormDeadlockReader) DeadlockCounter(ctx context.Context) (int64, error) {
	return deadlocks.DeadlockCounter(ctx, r.db)
}

func (r gormDeadlockReader) DispatchLatency(ctx context.Context) (time.Duration, error) {
	source, err := deadlocks.ResolveSource(ctx, r.db)
	return source.DispatchLatency, err
}

func (r gormDeadlockReader) LoadWindow(ctx context.Context, options deadlocks.ReadOptions) (deadlocks.Loaded, error) {
	return deadlocks.LoadWindow(ctx, r.db, options)
}

// OpenDeadlockReader leases a connection from db as a DeadlockReader; nil
// when db is.
func OpenDeadlockReader(db func(context.Context) (*gorm.DB, func(), error)) func(context.Context) (DeadlockReader, func(), error) {
	if db == nil {
		return nil
	}
	return func(ctx context.Context) (DeadlockReader, func(), error) {
		conn, release, err := db(ctx)
		if err != nil {
			return nil, nil, err
		}
		if conn == nil || release == nil {
			return nil, nil, errors.New("the database seam returned no connection or no release function")
		}
		return GormDeadlockReader(conn), release, nil
	}
}

// DeadlockSpan reads the deadlocks SQL Server resolved over a window. A follow
// span pins its start on the server clock and counter and reads up to the
// server's now; a historical one pins its whole window.
type DeadlockSpan struct {
	reader DeadlockReader
	sleep  func(context.Context, time.Duration) error
	scope  deadlocks.DatabaseScope
	follow bool

	from, to time.Time
	counter  int64
}

// DeadlockRead is one read of a span: the window read and the graphs in it,
// as graphs and as the rows a sql_deadlock stream stores.
type DeadlockRead struct {
	Result deadlocks.SpanResult
	Graphs []deadlockgraph.Graph
	Rows   []deadlocks.Row
}

// BeginDeadlockSpan pins a span over reader for the databases matched: a
// historical one when window has a From, a follow one from the server's now
// otherwise. A nil sleep waits on the wall clock.
func BeginDeadlockSpan(ctx context.Context, reader DeadlockReader, databases clicky.MultiFilter, window Window, sleep func(context.Context, time.Duration) error) (*DeadlockSpan, error) {
	if sleep == nil {
		sleep = sleepContext
	}
	s := &DeadlockSpan{reader: reader, sleep: sleep, scope: deadlocks.ScopeDatabases(databases), follow: window.From.IsZero()}
	return s, s.begin(ctx, window)
}

// Follow reports whether the span observes forward from its start.
func (s *DeadlockSpan) Follow() bool { return s.follow }

func (s *DeadlockSpan) begin(ctx context.Context, window Window) error {
	var err error
	if s.follow {
		if s.from, err = s.reader.ServerNow(ctx); err != nil {
			return err
		}
		s.counter, err = s.reader.DeadlockCounter(ctx)
		return err
	}
	s.from, s.to = window.From.UTC(), window.To.UTC()
	if window.To.IsZero() {
		if s.to, err = s.reader.ServerNow(ctx); err != nil {
			return err
		}
	}
	if !s.from.Before(s.to) {
		return fmt.Errorf("sql_deadlock window from %s is not before to %s", s.from.Format(time.RFC3339), s.to.Format(time.RFC3339))
	}
	return nil
}

// Read reads the span so far. It never waits for graphs to be flushed, so a
// deadlock resolved in the last dispatch latency may be counted but not yet
// read; Final reads it.
func (s *DeadlockSpan) Read(ctx context.Context) (DeadlockRead, error) {
	if !s.follow {
		return s.load(ctx, deadlocks.SpanResult{From: s.from, To: s.to})
	}
	result, err := s.observe(ctx)
	if err != nil {
		return DeadlockRead{}, err
	}
	return s.load(ctx, result)
}

// Final ends the span, waits for system_health to flush the graphs of any
// deadlock the counter saw, and reads them.
func (s *DeadlockSpan) Final(ctx context.Context) (DeadlockRead, error) {
	if !s.follow {
		return s.load(ctx, deadlocks.SpanResult{From: s.from, To: s.to})
	}
	result, err := s.observe(ctx)
	if err != nil {
		return DeadlockRead{}, err
	}
	if result.ServerDeadlocks > 0 {
		if result.Waited, err = s.settle(ctx, result); err != nil {
			return DeadlockRead{}, err
		}
	}
	return s.load(ctx, result)
}

// observe closes a follow span at the server's now and counts the deadlocks
// the instance resolved since it started.
func (s *DeadlockSpan) observe(ctx context.Context) (deadlocks.SpanResult, error) {
	now, err := s.reader.ServerNow(ctx)
	if err != nil {
		return deadlocks.SpanResult{}, err
	}
	counter, err := s.reader.DeadlockCounter(ctx)
	if err != nil {
		return deadlocks.SpanResult{}, err
	}
	if counter < s.counter {
		return deadlocks.SpanResult{}, fmt.Errorf("the Number of Deadlocks/sec counter fell from %d to %d during the span: "+
			"the instance restarted, so the span's deadlock count is unknown", s.counter, counter)
	}
	return deadlocks.SpanResult{From: s.from, To: now, Follow: true, ServerDeadlocks: counter - s.counter}, nil
}

// settle waits until the span's end plus system_health's dispatch latency has
// passed on the server clock, returning how long it waited.
func (s *DeadlockSpan) settle(ctx context.Context, result deadlocks.SpanResult) (time.Duration, error) {
	latency, err := s.reader.DispatchLatency(ctx)
	if err != nil {
		return 0, err
	}
	if latency == 0 {
		return 0, fmt.Errorf("system_health's MAX_DISPATCH_LATENCY is INFINITE: it writes events only when a buffer fills, "+
			"so no wait guarantees the %d deadlock graph(s) resolved during the span have reached its files", result.ServerDeadlocks)
	}
	now, err := s.reader.ServerNow(ctx)
	if err != nil {
		return 0, err
	}
	wait := result.To.Add(latency + sqlDeadlockDispatchMargin).Sub(now)
	if wait <= 0 {
		return 0, nil
	}
	if err := s.sleep(ctx, wait); err != nil {
		return 0, fmt.Errorf("waiting %s for system_health to flush %d deadlock graph(s): %w", wait, result.ServerDeadlocks, err)
	}
	return wait, nil
}

func (s *DeadlockSpan) load(ctx context.Context, result deadlocks.SpanResult) (DeadlockRead, error) {
	loaded, err := s.reader.LoadWindow(ctx, deadlocks.ReadOptions{
		Window:    deadlocks.Window{From: result.From, To: result.To},
		Databases: s.scope,
	})
	if err != nil {
		return DeadlockRead{}, err
	}
	rows, err := loaded.Rows()
	if err != nil {
		return DeadlockRead{}, err
	}
	return DeadlockRead{Result: result, Graphs: loaded.Graphs, Rows: rows}, nil
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
