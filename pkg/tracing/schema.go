package tracing

import (
	"strings"

	"github.com/flanksource/clicky/rpc"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

// DescribeConnection attaches the saved connection lookup to an argument.
func DescribeConnection(property *rpc.OpenAPISchema, types ...string) {
	endpoint := "/api/v1/connection"
	if len(types) > 0 {
		endpoint += "?types=" + strings.Join(types, ",")
	}
	if property.Extensions == nil {
		property.Extensions = map[string]any{}
	}
	property.Extensions["x-clicky-lookup"] = map[string]any{"url": endpoint, "filter": "connection", "searchParam": "__lookup_q"}
}

// SeamCondition matches an explicitly selected target seam.
func SeamCondition(seam target.Seam) *rpc.OpenAPISchema {
	return &rpc.OpenAPISchema{
		Required: []string{"target"},
		Properties: map[string]*rpc.OpenAPISchema{"target": {
			Required:   []string{"seam"},
			Properties: map[string]*rpc.OpenAPISchema{"seam": {Enum: []any{seam}}},
		}},
	}
}
