package read

import (
	"cmp"
	"fmt"
	sqlpages "github.com/flanksource/commons-db/query/providers/sqlite"
	"slices"
)

func OrderRecords(records []Record, order string) ([]Record, error) {
	if order != "seq" && order != "timestamp" {
		return nil, fmt.Errorf("unknown record sort %q", order)
	}
	rows := slices.Clone(records)
	var err error
	slices.SortStableFunc(rows, func(a, b Record) int {
		if order == "timestamp" && a.Timestamp != nil && b.Timestamp != nil {
			compared, compareErr := sqlpages.CompareSQLite(a.Timestamp, b.Timestamp)
			if compareErr != nil {
				err = compareErr
				return 0
			}
			if compared != 0 {
				return compared
			}
		}
		if order == "timestamp" {
			if a.Timestamp == nil && b.Timestamp != nil {
				return 1
			}
			if b.Timestamp == nil && a.Timestamp != nil {
				return -1
			}
		}
		if compared := cmp.Compare(a.Seq, b.Seq); compared != 0 {
			return compared
		}
		if compared := cmp.Compare(a.Kind, b.Kind); compared != 0 {
			return compared
		}
		if compared := cmp.Compare(a.RunID, b.RunID); compared != 0 {
			return compared
		}
		return cmp.Compare(a.Stream, b.Stream)
	})
	return rows, err
}
