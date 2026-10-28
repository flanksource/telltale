package deadlock

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/flanksource/clicky"
	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/recordstore/recordresults"
	"github.com/flanksource/commons/collections"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/sqlserver/history"
	"github.com/flanksource/telltale/pkg/tracing/sqlserver/xeprovider"
	"github.com/flanksource/telltale/pkg/tracing/sqlserver/xetrace"
	"github.com/flanksource/telltale/pkg/tracing/target"
	"gorm.io/driver/sqlserver"
	"gorm.io/gorm"
)

type Window struct{ From, To time.Time }

type Params struct {
	Target     target.Target `json:"target,omitzero"`
	Connection string        `json:"connection" clicky:"required,title=Connection"`
	Databases  []string      `json:"databases,omitempty" clicky:"title=Databases,desc=Participant database patterns; empty selects the connected database"`
	From       string        `json:"from,omitempty" clicky:"title=From,desc=Historical window start in RFC3339; empty follows from now"`
	To         string        `json:"to,omitempty" clicky:"title=To,desc=Historical window end in RFC3339; empty uses server time"`
	Poll       string        `json:"poll,omitempty" clicky:"title=Poll,desc=Follow poll interval"`
}

func (p Params) window() (Window, error) {
	var w Window
	var err error
	if p.From != "" {
		w.From, err = time.Parse(time.RFC3339Nano, p.From)
		if err != nil {
			return w, fmt.Errorf("from: %w", err)
		}
	}
	if p.To != "" {
		if p.From == "" {
			return w, fmt.Errorf("to requires from")
		}
		w.To, err = time.Parse(time.RFC3339Nano, p.To)
		if err != nil {
			return w, fmt.Errorf("to: %w", err)
		}
	}
	if !w.To.IsZero() && !w.From.Before(w.To) {
		return w, fmt.Errorf("from must precede to")
	}
	return w, nil
}

func (p Params) Validate() error {
	if strings.TrimSpace(p.Connection) == "" {
		return fmt.Errorf("connection is required")
	}
	if _, err := p.window(); err != nil {
		return err
	}
	if p.Poll != "" {
		d, err := time.ParseDuration(p.Poll)
		if err != nil || d <= 0 {
			return fmt.Errorf("poll %q must be a positive duration", p.Poll)
		}
	}
	return xetrace.ValidateMatchPatterns(p.Databases)
}

type capture struct {
	mu        sync.Mutex
	span      *DeadlockSpan
	seen      map[string]bool
	victims   int
	databases []string
	poll      time.Duration
}
type captureKey struct{}
type handler struct{}

func (handler) Params() Params { return Params{Poll: "30s"} }
func (handler) Schema() recordresults.ResultType[history.Row] {
	return recordresults.ResultType[history.Row]{Title: "SQL Server deadlocks", TimeColumn: "timestamp", Follow: true, SearchColumns: []string{"statements", "signature"}}
}

func (handler) Prepare(ctx dbcontext.Context, p Params, out tracing.Emitter[history.Row]) (dbcontext.Context, func() error, error) {
	pool, release, err := xeprovider.OpenSQLServer(ctx, p.Connection)
	if err != nil {
		return ctx, nil, err
	}
	db, err := gorm.Open(sqlserver.New(sqlserver.Config{Conn: pool}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		release()
		return ctx, nil, err
	}
	window, err := p.window()
	if err != nil {
		release()
		return ctx, nil, err
	}
	span, err := BeginDeadlockSpan(ctx, GormDeadlockReader(db), clicky.MultiFilter(p.Databases), window, nil)
	if err != nil {
		release()
		return ctx, nil, err
	}
	poll := 30 * time.Second
	if p.Poll != "" {
		poll, err = time.ParseDuration(p.Poll)
		if err != nil {
			release()
			return ctx, nil, err
		}
	}
	out.Describe("system_health deadlocks")
	return ctx.WithValue(captureKey{}, &capture{span: span, seen: map[string]bool{}, databases: p.Databases, poll: poll}), func() error { release(); return nil }, nil
}

func (c *capture) read(ctx context.Context, out tracing.Emitter[history.Row], final bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var result DeadlockRead
	var err error
	if final {
		result, err = c.span.Final(ctx)
	} else {
		result, err = c.span.Read(ctx)
	}
	if err != nil {
		return err
	}
	for _, row := range result.Rows {
		if c.seen[row.ID] {
			continue
		}
		if len(c.databases) > 0 && !slices.ContainsFunc(row.Databases, func(name string) bool { return collections.MatchItems(name, c.databases...) }) {
			continue
		}
		if err := out.Emit(ctx, row); err != nil {
			return err
		}
		c.seen[row.ID] = true
		c.victims += row.Victims
	}
	result.Result.Victims = c.victims
	out.Report(result.Result)
	return nil
}

func (handler) Handle(ctx dbcontext.Context, _ Params, out tracing.Emitter[history.Row], _ tracing.Records[history.Row]) error {
	c := ctx.Value(captureKey{}).(*capture)
	if !c.span.Follow() {
		return c.read(ctx, out, true)
	}
	ticker := time.NewTicker(c.poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return c.read(context.WithoutCancel(ctx), out, true)
		case <-ticker.C:
			if err := c.read(ctx, out, false); err != nil {
				return err
			}
		}
	}
}

func (handler) Checkpoint(ctx dbcontext.Context, _ Params, out tracing.Emitter[history.Row]) error {
	return ctx.Value(captureKey{}).(*capture).read(ctx, out, false)
}
func Kind() *tracing.Handler[Params, history.Row] {
	return tracing.NewHandler[Params, history.Row](handler{}, tracing.Capabilities{Historical: true, Follow: true}, target.Connection).WithDeduplication(func(r history.Row) string { return r.ID }, func(Params) time.Duration { return 24 * time.Hour })
}
