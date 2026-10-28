package plancache

import (
	"database/sql"

	"gorm.io/driver/sqlserver"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func openDB(pool *sql.DB) (*gorm.DB, error) {
	return gorm.Open(sqlserver.New(sqlserver.Config{Conn: pool}), &gorm.Config{DisableAutomaticPing: true, Logger: logger.Discard})
}
