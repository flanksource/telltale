package sqltop

import (
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/telltale/pkg/tracing"
)

func topSpec() tracing.TopSpec {
	return tracing.TopSpec{Relation: &tracing.TopRelation{Column: "blockedBy", Target: "sessionId", Label: "Blocker"}, Identity: []string{"sessionId", "requestId"}, Order: query.Order{{Column: "elapsedMs", Desc: true}, {Column: "sessionId"}, {Column: "requestId"}}}
}
