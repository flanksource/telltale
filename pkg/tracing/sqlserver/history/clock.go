package history

import (
	"context"
	"fmt"
	"gorm.io/gorm"
	"time"
)

type SpanResult struct {
	From            time.Time     `json:"from"`
	To              time.Time     `json:"to"`
	Follow          bool          `json:"follow"`
	ServerDeadlocks int64         `json:"serverDeadlocks"`
	Victims         int           `json:"victims"`
	Waited          time.Duration `json:"waitedNs"`
}

func ServerNow(ctx context.Context, db *gorm.DB) (time.Time, error) {
	var rows []struct{ Now time.Time }
	if err := db.WithContext(ctx).Raw("SELECT SYSUTCDATETIME() AS now").Scan(&rows).Error; err != nil {
		return time.Time{}, fmt.Errorf("read server clock: %w", err)
	}
	if len(rows) != 1 {
		return time.Time{}, fmt.Errorf("server clock returned %d rows, want 1", len(rows))
	}
	return rows[0].Now.UTC(), nil
}
