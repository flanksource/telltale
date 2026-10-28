package plancache

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/flanksource/clicky"
	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/sqlserver/xeprovider"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

type Params struct {
	Target        target.Target      `json:"target,omitzero"`
	Connection    string             `json:"connection" clicky:"required,title=Connection,desc=SQL Server connection reference"`
	Databases     clicky.MultiFilter `json:"databases,omitempty" clicky:"title=Databases,desc=Database patterns with * wildcards and ! exclusions; empty selects the connected database"`
	Poll          string             `json:"poll,omitempty" clicky:"title=Poll,desc=Poll interval such as 30s"`
	Top           int                `json:"top,omitempty" clicky:"title=Top,desc=Maximum cost samples per poll ranked by interval CPU; defaults to 200"`
	MinExecutions int                `json:"minExecutions,omitempty" clicky:"title=Minimum executions,desc=Minimum interval executions to record"`
	Text          clicky.MultiFilter `json:"text,omitempty" clicky:"title=Statement text,desc=SQL text patterns with * wildcards and ! exclusions; bare values match substrings"`
}

func (p Params) pollInterval() (time.Duration, error) {
	if p.Poll == "" {
		return 30 * time.Second, nil
	}
	d, err := time.ParseDuration(p.Poll)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("poll %q must be a positive duration", p.Poll)
	}
	return d, nil
}

func (p Params) Validate() error {
	if strings.TrimSpace(p.Connection) == "" {
		return fmt.Errorf("connection is required")
	}
	if _, err := p.pollInterval(); err != nil {
		return err
	}
	if p.Top < 0 {
		return fmt.Errorf("top must be non-negative")
	}
	if p.MinExecutions < 0 {
		return fmt.Errorf("minExecutions must be non-negative")
	}
	if err := validatePatterns(p.Databases); err != nil {
		return err
	}
	return validatePatterns(p.Text)
}

type handler struct{}
type captureKey struct{}

func (handler) Params() Params { return Params{Poll: "30s", Top: 200} }

func (handler) Prepare(ctx dbcontext.Context, p Params, out tracing.Emitter[Sample]) (dbcontext.Context, func() error, error) {
	if err := p.Validate(); err != nil {
		return ctx, nil, err
	}
	pool, release, err := xeprovider.OpenSQLServer(ctx, p.Connection)
	if err != nil {
		return ctx, nil, err
	}
	db, err := openDB(pool)
	if err != nil {
		release()
		return ctx, nil, err
	}
	c := &capture{params: p, now: time.Now, reader: func(ctx context.Context, options snapshotOptions) (cacheSnapshot, error) {
		return readSnapshot(ctx, db, options)
	}}
	if err := c.poll(ctx, out); err != nil {
		release()
		return ctx, nil, err
	}
	out.Describe("SQL Server plan cache interval costs")
	return ctx.WithValue(captureKey{}, c), func() error { release(); return nil }, nil
}

func (handler) Handle(ctx dbcontext.Context, p Params, out tracing.Emitter[Sample], _ tracing.Records[Sample]) error {
	c, ok := ctx.Value(captureKey{}).(*capture)
	if !ok {
		return fmt.Errorf("plan cache capture was not prepared")
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
	c, ok := ctx.Value(captureKey{}).(*capture)
	if !ok {
		return fmt.Errorf("plan cache capture was not prepared")
	}
	return c.poll(ctx, out)
}

func Kind() *tracing.Handler[Params, Sample] {
	return tracing.NewHandler[Params, Sample](handler{}, tracing.Capabilities{Follow: true}, target.Connection)
}
