// Package sql is the sql trace kind: it records the SQL
// statements commons-db runs on chosen connections, as the SQL statement tap
// publishes them, and summarizes them as it goes.
package sql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/tracing/seam"
	"github.com/flanksource/telltale/pkg/tracing/target"
	_ "github.com/flanksource/telltale/sdk/commonsdb"

	"github.com/flanksource/commons-db/connection"
	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/commons-db/recordstore/recordresults"
	"github.com/flanksource/telltale/pkg/tracing"
)

const reportEvery = time.Second

// Params choose the statements a capture records.
type Params struct {
	// Connections name the connections to observe, as profiles reference
	// them, "*" for every connection, or "self" for the server's own
	// database, which "*" does not include.
	Connections []string `json:"connections" clicky:"required,title=Connections,desc=Connections to observe: names; * for every one; or self for telltale's own database"`
	// MinDuration, a duration such as 100ms, records only statements that ran
	// at least that long.
	MinDuration string        `json:"minDuration,omitempty" clicky:"title=Minimum duration,desc=Record only statements that ran at least this long such as 100ms"`
	Target      target.Target `json:"target,omitzero"`
}

func (p Params) Validate() error {
	if len(p.Connections) == 0 {
		return errors.New(`connections is required: name connections, "*" for every connection, or "self" for the server's own database`)
	}
	for _, name := range p.Connections {
		if strings.TrimSpace(name) == "" {
			return errors.New("connections must not name a blank connection")
		}
	}
	_, err := p.minimum()
	return err
}

func (p Params) minimum() (time.Duration, error) {
	if p.MinDuration == "" {
		return 0, nil
	}
	minimum, err := time.ParseDuration(p.MinDuration)
	if err != nil || minimum < 0 {
		return 0, fmt.Errorf("minDuration %q is not a duration such as 100ms", p.MinDuration)
	}
	return minimum, nil
}

// repeatsView serves each statement text that ran more than once as one row:
// how often, and what its runs took.
var repeatsView = recordresults.ResultView{
	Name: "repeats", Title: "Repeated SQL statements",
	Query: `SELECT repeatKey, min(sql) AS sql, count(*) AS runs,
			sum(durationMs) AS durationMs, max(durationMs) AS maxDurationMs
		FROM stream_rows GROUP BY repeatKey HAVING count(*) > 1`,
	Columns: []query.ColumnDef{
		{Name: "repeatKey", Type: query.ColumnTypeString, Hidden: true},
		{Name: "sql", Label: "Statement", Type: query.ColumnTypeString},
		{Name: "runs", Label: "Runs", Type: query.ColumnTypeNumber},
		{Name: "durationMs", Label: "Total", Type: query.ColumnTypeDuration, Unit: "ms"},
		{Name: "maxDurationMs", Label: "Longest", Type: query.ColumnTypeDuration, Unit: "ms"},
	},
	Order: query.Order{{Column: "runs", Desc: true}, {Column: "repeatKey", Unique: true}},
}

// running is what Prepare set up for Handle: the summary of the statements
// the capture stored, which the goroutines running them fold in.
type running struct {
	mu       sync.Mutex
	summary  *Accumulator
	reported time.Time
}

func (r *running) result() Summary {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.summary.Result()
}

type runningKey struct{}

type statements struct {
	agent seam.AgentSeam[Params, StatementRow]
}

func (s statements) AgentAvailable(probe string, selection target.Target) (bool, error) {
	return s.agent.Available(probe, selection)
}

func (statements) Params() Params { return Params{} }

func (statements) Schema() recordresults.ResultType[StatementRow] { return ResultType() }

// ResultType is the RowKind result type: it follows a capture's stream while
// it runs, searches the text a reader remembers of a statement, or of its
// error, and serves the repeats view. A host that registers result types
// itself rather than through traces.Kinds registers this one.
func ResultType() recordresults.ResultType[StatementRow] {
	return recordresults.ResultType[StatementRow]{
		Kind: RowKind, Title: "SQL statements", TimeColumn: "startedAt", Follow: true,
		SearchColumns: []string{"sql", "error"},
		Views:         []recordresults.ResultView{repeatsView},
	}
}

// Prepare observes the connections before the session runs, emitting each
// statement without waiting: it arrives on the goroutine that ran it. The
// release stops observing and reports the Summary of every statement stored.
func (s statements) Prepare(ctx dbcontext.Context, params Params, records tracing.Emitter[StatementRow]) (dbcontext.Context, func() error, error) {
	capture := &running{summary: NewAccumulator()}
	prepared, release, err := s.agent.Prepare(ctx, params, records)
	if err != nil {
		return ctx, nil, err
	}
	return prepared.WithValue(runningKey{}, capture), func() error {
		err := release()
		records.Report(capture.result())
		return err
	}, nil
}

type summaryEmitter struct {
	tracing.Emitter[StatementRow]
	capture *running
}

func (e summaryEmitter) Emit(ctx context.Context, row StatementRow) error {
	if err := e.Emitter.Emit(ctx, row); err != nil {
		return err
	}
	e.capture.mu.Lock()
	e.capture.summary.Add(row)
	if time.Since(e.capture.reported) >= reportEvery {
		e.capture.reported = time.Now()
		summary := e.capture.summary.Result()
		e.capture.mu.Unlock()
		e.Report(summary)
		return nil
	}
	e.capture.mu.Unlock()
	return nil
}

// Handle reads statements from the armed agent until the capture stops.
func (s statements) Handle(ctx dbcontext.Context, params Params, records tracing.Emitter[StatementRow], _ tracing.Records[StatementRow]) error {
	capture, ok := ctx.Value(runningKey{}).(*running)
	if !ok {
		return errors.New("sql: the capture was not prepared")
	}
	return s.agent.Handle(ctx, params, summaryEmitter{Emitter: records, capture: capture})
}

// Kind is the sql kind: statements masked and capped at 64KiB a
// value. A host registers it as RowKind.
func NewCapture(registry *agentclient.Registry) interface {
	tracing.TraceHandler[Params, StatementRow]
	tracing.Preparer[Params, StatementRow]
	AgentAvailable(string, target.Target) (bool, error)
} {
	agent := seam.AgentSeam[Params, StatementRow]{Probe: RowKind, Registry: registry,
		Target:    func(p Params) target.Target { return p.Target },
		ArmParams: func(p Params) (json.RawMessage, error) { return json.Marshal(p) },
		DecodeRecord: func(raw json.RawMessage) (StatementRow, error) {
			var statement connection.Statement
			if err := json.Unmarshal(raw, &statement); err != nil {
				return StatementRow{}, err
			}
			return FromStatement(statement), nil
		},
	}
	return statements{agent: agent}
}
