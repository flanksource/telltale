package sqltop

import (
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/telltale/pkg/tracing"
)

func topSpec() tracing.TopSpec {
	return tracing.TopSpec{Result: "active", PerSource: true, Relation: &tracing.TopRelation{Column: "blockedByOperationId", Target: "operationId", Label: "Blocker"}, Identity: []string{"source", "operationId"}, Order: query.Order{{Column: "elapsedMs", Desc: true}, {Column: "operationId"}}}
}
