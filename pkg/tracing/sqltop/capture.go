package sqltop

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/flanksource/commons/collections"
	"github.com/flanksource/telltale/pkg/tracing"
)

// snapshot is one read of an engine: the server's time and its requests.
type snapshot struct {
	ServerTime time.Time
	Rows       []Sample
}

type capture struct {
	mu         sync.Mutex
	options    Options
	engine     string
	read       func(context.Context, bool) (snapshot, error)
	databases  []string
	text       []string
	minElapsed time.Duration
	polls      int
}

func newCapture(options Options, engine string, read func(context.Context, bool) (snapshot, error)) (*capture, error) {
	minElapsed, err := options.minElapsed()
	if err != nil {
		return nil, err
	}
	return &capture{options: options, engine: engine, read: read, minElapsed: minElapsed,
		databases: normalizePatterns(options.Databases), text: substringPatterns(options.Text)}, nil
}

// poll reads the engine once and emits the busiest requests it keeps, by
// elapsed time, at most options.Top of them.
func (c *capture) poll(ctx context.Context, out tracing.Emitter[Sample]) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	result, err := c.read(ctx, c.options.Idle)
	if err != nil {
		return fmt.Errorf("sample %s requests: %w", c.engine, err)
	}
	rows, err := c.keep(result)
	if err != nil {
		return err
	}
	active, blocked := len(rows), 0
	for _, row := range rows {
		if row.Blocked {
			blocked++
		}
	}
	slices.SortStableFunc(rows, func(a, b Sample) int {
		return cmp.Or(cmp.Compare(b.ElapsedMs, a.ElapsedMs), cmp.Compare(a.SessionID, b.SessionID), cmp.Compare(a.RequestID, b.RequestID))
	})
	if len(rows) > c.options.top() {
		rows = rows[:c.options.top()]
	}
	for _, row := range rows {
		if err := out.Emit(ctx, row); err != nil {
			return err
		}
	}
	c.polls++
	out.Report(map[string]any{"engine": c.engine, "polls": c.polls, "active": active, "blocked": blocked, "recorded": len(rows)})
	return nil
}

// keep stamps every request with the server time and drops those the options
// filter out, refusing a snapshot without a server time or with a request
// twice.
func (c *capture) keep(result snapshot) ([]Sample, error) {
	if result.ServerTime.IsZero() {
		return nil, fmt.Errorf("%s snapshot has no server timestamp", c.engine)
	}
	seen := map[[2]int64]bool{}
	var rows []Sample
	for _, row := range result.Rows {
		key := [2]int64{row.SessionID, row.RequestID}
		if seen[key] {
			return nil, fmt.Errorf("%s snapshot has session %d request %d twice", c.engine, row.SessionID, row.RequestID)
		}
		seen[key] = true
		row.Engine = c.engine
		row.derive(result.ServerTime)
		if len(c.databases) > 0 && !collections.MatchItems(row.Database, c.databases...) {
			continue
		}
		if len(c.text) > 0 && !collections.MatchItems(row.SQL, c.text...) {
			continue
		}
		if row.ElapsedMs < milliseconds(c.minElapsed) {
			continue
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func normalizePatterns(values []string) []string {
	var out []string
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

func validatePatterns(name string, values []string) error {
	for _, pattern := range normalizePatterns(values) {
		if pattern == "!" {
			return fmt.Errorf("%s pattern %q excludes no value", name, pattern)
		}
	}
	return nil
}

// substringPatterns makes each bare text pattern match anywhere in the
// statement, as a pattern with its own wildcards matches as written.
func substringPatterns(values []string) []string {
	patterns := normalizePatterns(values)
	for i, pattern := range patterns {
		value := strings.TrimPrefix(pattern, "!")
		if !strings.Contains(value, "*") {
			patterns[i] = strings.Replace(pattern, value, "*"+value+"*", 1)
		}
	}
	return patterns
}
