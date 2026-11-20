package sql

import (
	"errors"
	"time"

	"github.com/flanksource/clicky"
	"github.com/flanksource/clicky/api"
	dbgorm "github.com/flanksource/commons-db/gorm"
	"github.com/flanksource/commons/properties"
)

func (r StatementRow) Pretty() api.Text {
	duration := time.Duration(r.DurationMs * float64(time.Millisecond))
	threshold := properties.Duration(time.Second, "log.db.slowThreshold")
	trace := dbgorm.SQLTrace{SQL: r.SQL, Duration: duration, Rows: r.Rows, Slow: threshold > 0 && duration > threshold}
	if r.Error != "" {
		trace.Err = errors.New(r.Error)
	}
	return trace.Pretty()
}

func (r StatementRow) PrettyFull() api.Textable {
	t := r.Pretty().NewLine().Add(clicky.CodeBlock("sql", r.SQL))
	if len(r.Args) > 0 {
		t = t.NewLine().Add(clicky.Map(map[string]any{"Arguments": r.Args}))
	}
	return t
}
