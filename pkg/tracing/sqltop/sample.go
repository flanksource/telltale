package sqltop

import "github.com/flanksource/commons-db/query/sqlactivity"

type Sample = sqlactivity.Sample

const (
	EnginePostgres  = sqlactivity.EnginePostgres
	EngineSQLServer = sqlactivity.EngineSQLServer
)
