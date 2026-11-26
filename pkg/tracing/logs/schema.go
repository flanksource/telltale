package logs

import (
	"maps"
	"slices"

	"github.com/flanksource/clicky/rpc"
	"github.com/flanksource/commons-db/query/schema"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

var cloudConnectionTypes = map[string]string{
	"cloudwatch": "aws", "gcpcloudlogging": "gcp", "azureloganalytics": "azure",
	"loki": "loki", "opensearch": "opensearch",
}

func (capture) DescribeSchema(args *rpc.OpenAPISchema) {
	fields := args.Properties
	fields["source"].Title = "Source"
	fields["source"].Enum = []any{"container", "kubernetes", "cloud"}
	fields["source"].Extensions = map[string]any{"x-clicky-order": -1}
	fields["backend"].Title = "Backend"
	fields["connection"].Title = "Connection"
	backends := slices.Sorted(maps.Keys(cloudConnectionTypes))
	for _, backend := range backends {
		fields["backend"].Enum = append(fields["backend"].Enum, backend)
	}
	fields["namespace"].Title = "Namespace"
	fields["podSelector"].Title = "Pod selector"
	fields["options"].AdditionalProperties = &rpc.OpenAPISchema{}
	providerOptions := make(map[string]schema.Schema, len(backends))
	scoped := []string{"backend", "connection", "container", "namespace", "podSelector", "query", "options", "timeField", "minLevel"}
	conditions := []any{
		map[string]any{"if": tracing.SeamCondition(target.Host),
			"then": map[string]any{"required": []string{"source"}, "properties": hiddenFields(fields, append(slices.Clone(scoped), "levels")...),
				"not": map[string]any{"required": []string{"levels"}, "properties": map[string]any{"levels": map[string]int{"minProperties": 1}}}},
			"else": map[string]any{"not": map[string]any{"required": []string{"source"}}, "properties": hiddenFields(fields, append(slices.Clone(scoped), "source")...)}},
		logSourceRule(fields, "container", []string{"container"}),
		logSourceRule(fields, "kubernetes", []string{"namespace", "podSelector"}, "container"),
		logSourceRule(fields, "cloud", []string{"backend", "connection"}, "query", "timeField", "minLevel"),
	}
	for _, backend := range backends {
		options := schema.BrowserOptions(backend)
		providerOptions[backend] = options
		connection := *fields["connection"]
		connection.Extensions = maps.Clone(connection.Extensions)
		tracing.DescribeConnection(&connection, cloudConnectionTypes[backend])
		conditions = append(conditions, map[string]any{
			"if":   hostLogCondition(map[string]string{"source": "cloud", "backend": backend}),
			"then": map[string]any{"properties": map[string]any{"options": options, "connection": &connection}},
		})
	}
	structuredSearch := hostLogCondition(map[string]string{"source": "cloud", "backend": "opensearch"})
	structuredSearch.Required = append(structuredSearch.Required, "options")
	structuredSearch.Properties["options"] = &rpc.OpenAPISchema{
		Required:   []string{"search"},
		Properties: map[string]*rpc.OpenAPISchema{"search": {Type: "object"}},
	}
	conditions = append(conditions, map[string]any{
		"if": structuredSearch, "then": map[string]any{"properties": map[string]any{"query": map[string]string{"pattern": "^\\s*$"}}},
	})
	fields["options"].Extensions = map[string]any{"x-telltale-provider-options": providerOptions}
	args.Extensions["allOf"] = conditions
}

func logSourceRule(fields map[string]*rpc.OpenAPISchema, source string, required []string, optional ...string) map[string]any {
	properties := make(map[string]*rpc.OpenAPISchema, len(required)+len(optional))
	for _, field := range optional {
		properties[field] = fields[field]
	}
	for _, field := range required {
		copy := *fields[field]
		copy.Extensions = maps.Clone(copy.Extensions)
		if copy.Extensions == nil {
			copy.Extensions = map[string]any{}
		}
		copy.Extensions["minLength"] = 1
		properties[field] = &copy
	}
	if source == "kubernetes" {
		connection := *fields["connection"]
		connection.Extensions = maps.Clone(connection.Extensions)
		tracing.DescribeConnection(&connection, "kubernetes")
		properties["connection"] = &connection
	}
	if source != "cloud" {
		properties["minLevel"] = &rpc.OpenAPISchema{Extensions: map[string]any{"const": "", "x-hidden": true}}
	}
	return map[string]any{"if": hostLogCondition(map[string]string{"source": source}), "then": map[string]any{"required": required, "properties": properties}}
}

func hostLogCondition(fields map[string]string) *rpc.OpenAPISchema {
	condition := tracing.SeamCondition(target.Host)
	for _, field := range slices.Sorted(maps.Keys(fields)) {
		condition.Required = append(condition.Required, field)
		condition.Properties[field] = &rpc.OpenAPISchema{Extensions: map[string]any{"const": fields[field]}}
	}
	return condition
}

func hiddenFields(fields map[string]*rpc.OpenAPISchema, names ...string) map[string]*rpc.OpenAPISchema {
	properties := make(map[string]*rpc.OpenAPISchema, len(names))
	for _, name := range names {
		field := *fields[name]
		field.Extensions = map[string]any{"x-hidden": true}
		properties[name] = &field
	}
	return properties
}
