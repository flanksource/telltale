package records

import "github.com/flanksource/telltale/pkg/traces/read"

type styledRecord struct {
	read.Record
	ANSI string `json:"ansi"`
}

func presentFeedRecord(record read.Record, presentation string) any {
	if presentation == "ansi" {
		return styledRecord{Record: record, ANSI: record.Pretty().ANSI()}
	}
	return record
}

func presentFeedPage(page read.FeedResult, presentation string) any {
	if presentation != "ansi" {
		return page
	}
	rows := make([]any, len(page.Rows))
	for i, row := range page.Rows {
		rows[i] = presentFeedRecord(row, presentation)
	}
	return struct {
		Rows       []any  `json:"rows"`
		NextCursor string `json:"nextCursor"`
	}{Rows: rows, NextCursor: page.NextCursor}
}
