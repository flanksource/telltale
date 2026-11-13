package sqltop

import (
	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/commons-db/recordstore/recordresults"
	"maps"
	"slices"
)

func (handler) ConnectionTypes() []string { return slices.Sorted(maps.Keys(engines)) }

func (handler) Schema() recordresults.ResultType[Sample] {
	return recordresults.ResultType[Sample]{Title: "SQL running requests", TimeColumn: "sampledAt", Follow: true,
		Indexes:       []recordstore.IndexDef{{Columns: []string{"sessionId", "sampledAt"}}},
		SearchColumns: []string{"sql", "user", "application"}}
}
