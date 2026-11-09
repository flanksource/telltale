package sqltop

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/flanksource/clicky"
	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/models"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/sqlconn"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

const (
	defaultPoll = 5 * time.Second
	defaultTop  = 50
)

type Params struct {
	Target target.Target `json:"target,omitzero"`
	Options
}

// Options are the connection whose running requests a capture samples, which
// of them it keeps, and how often and how many it records.
type Options struct {
	Connection string             `json:"connection" clicky:"required,title=Connection,desc=Postgres or SQL Server connection reference"`
	Databases  clicky.MultiFilter `json:"databases,omitempty" clicky:"title=Databases,desc=Database patterns with * wildcards and ! exclusions; empty keeps every database"`
	Text       clicky.MultiFilter `json:"text,omitempty" clicky:"title=Statement text,desc=SQL text patterns with * wildcards and ! exclusions; bare values match substrings"`
	Poll       string             `json:"poll,omitempty" clicky:"title=Poll,desc=Poll interval such as 5s"`
	Top        int                `json:"top,omitempty" clicky:"title=Top,desc=Maximum requests per poll ranked by elapsed time; defaults to 50"`
	MinElapsed string             `json:"minElapsed,omitempty" clicky:"title=Minimum elapsed,desc=Only record requests running at least this long such as 500ms"`
	Idle       bool               `json:"idle,omitempty" clicky:"title=Idle sessions,desc=Also record idle sessions; sessions holding an open transaction are always recorded"`
}

func (o Options) pollInterval() (time.Duration, error) {
	return positiveDuration("poll", o.Poll, defaultPoll)
}

func (o Options) minElapsed() (time.Duration, error) {
	if o.MinElapsed == "" {
		return 0, nil
	}
	return positiveDuration("minElapsed", o.MinElapsed, 0)
}

func (o Options) top() int {
	if o.Top == 0 {
		return defaultTop
	}
	return o.Top
}

func positiveDuration(name, value string, empty time.Duration) (time.Duration, error) {
	if value == "" {
		return empty, nil
	}
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s %q must be a positive duration", name, value)
	}
	return d, nil
}

// Validate is the static check of the options: it opens no connection.
func (o Options) Validate() error {
	if strings.TrimSpace(o.Connection) == "" {
		return fmt.Errorf("connection is required")
	}
	if _, err := o.pollInterval(); err != nil {
		return err
	}
	if _, err := o.minElapsed(); err != nil {
		return err
	}
	if o.Top < 0 {
		return fmt.Errorf("top must be non-negative")
	}
	if err := validatePatterns("databases", o.Databases); err != nil {
		return err
	}
	return validatePatterns("text", o.Text)
}

type handler struct{}
type captureKey struct{}

func (handler) Params() Params { return Params{Options: Options{Poll: defaultPoll.String(), Top: defaultTop}} }

// readers are the engine readers by connection type.
var readers = map[string]struct {
	engine string
	read   func(context.Context, *sql.DB, bool) (snapshot, error)
}{
	models.ConnectionTypePostgres:  {EnginePostgres, readPostgres},
	models.ConnectionTypeSQLServer: {EngineSQLServer, readSQLServer},
}

func (handler) Prepare(ctx dbcontext.Context, p Params, out tracing.Emitter[Sample]) (dbcontext.Context, func() error, error) {
	if err := p.Validate(); err != nil {
		return ctx, nil, err
	}
	pool, connType, release, err := sqlconn.Open(ctx, sqlconn.OpenOptions{Reference: p.Connection,
		Types: []string{models.ConnectionTypePostgres, models.ConnectionTypeSQLServer}, Purpose: "SQL top"})
	if err != nil {
		return ctx, nil, err
	}
	reader := readers[connType]
	c, err := newCapture(p.Options, reader.engine, func(ctx context.Context, idle bool) (snapshot, error) {
		return reader.read(ctx, pool, idle)
	})
	if err != nil {
		release()
		return ctx, nil, err
	}
	if err := c.poll(ctx, out); err != nil {
		release()
		return ctx, nil, err
	}
	out.Describe(fmt.Sprintf("%s running requests on %s", reader.engine, p.Connection))
	return ctx.WithValue(captureKey{}, c), func() error { release(); return nil }, nil
}

func (handler) Handle(ctx dbcontext.Context, p Params, out tracing.Emitter[Sample], _ tracing.Records[Sample]) error {
	c, err := preparedCapture(ctx)
	if err != nil {
		return err
	}
	interval, err := p.pollInterval()
	if err != nil {
		return err
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			final, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			return c.poll(final, out)
		case <-ticker.C:
			if err := c.poll(ctx, out); err != nil {
				if ctx.Err() != nil {
					continue
				}
				return err
			}
		}
	}
}

func (handler) Checkpoint(ctx dbcontext.Context, _ Params, out tracing.Emitter[Sample]) error {
	var params struct {
		Reason string `json:"reason,omitempty"`
	}
	if _, err := tracing.DecodeCheckpointParams(ctx, &params); err != nil {
		return err
	}
	c, err := preparedCapture(ctx)
	if err != nil {
		return err
	}
	return c.poll(ctx, out)
}

func preparedCapture(ctx dbcontext.Context) (*capture, error) {
	c, ok := ctx.Value(captureKey{}).(*capture)
	if !ok {
		return nil, fmt.Errorf("sql top capture was not prepared")
	}
	return c, nil
}

func Kind() *tracing.Handler[Params, Sample] {
	return tracing.NewHandler[Params, Sample](handler{}, tracing.Capabilities{Follow: true}, target.Connection)
}
