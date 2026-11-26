package sqltop

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/flanksource/commons-db/query/sqlactivity"
)

func databaseReader(ctx context.Context, pool *sql.DB, engine string) (func(context.Context, bool) (snapshot, error), error) {
	var readMetrics func(context.Context) (serverReading, error)
	switch engine {
	case EnginePostgres:
		metrics, err := newPostgresMetrics(ctx, pool)
		if err != nil {
			return nil, err
		}
		readMetrics = metrics.read
	case EngineSQLServer:
		readMetrics = (sqlServerMetrics{pool: pool}).read
	default:
		return nil, fmt.Errorf("unsupported SQL top engine %q", engine)
	}
	return func(ctx context.Context, idle bool) (snapshot, error) {
		activity, err := sqlactivity.Read(ctx, pool, engine, sqlactivity.Filter{Idle: idle, SQLServerProgress: engine == EngineSQLServer})
		if err != nil {
			return snapshot{}, fmt.Errorf("read requests: %w", err)
		}
		metrics, err := readMetrics(ctx)
		if err != nil {
			return snapshot{}, err
		}
		return snapshot{ServerTime: activity.ServerTime, Rows: activity.Rows, Metrics: &metrics}, nil
	}, nil
}
