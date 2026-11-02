package telltale

import (
	"fmt"
	"slices"

	"github.com/flanksource/clicky/rpc"
	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/tracing/httpproxy"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

// ArmableKinds are the registered kinds the start form offers
// (traces.Registration.Armable), sorted by name. A start request may arm any
// registered kind; these are the ones its form shows.
func ArmableKinds() []traces.Registration {
	var armable []traces.Registration
	for _, registration := range traces.Registrations() {
		if registration.Armable {
			armable = append(armable, registration)
		}
	}
	return armable
}

// StartSchema is the JSON schema of a StartRequest, the form a client renders
// to start a run, and the values it starts from: each armable kind's params
// shown once the kind is enabled, HTTP proxy enabled as `trace run` arms it.
func StartSchema() (*rpc.OpenAPISchema, map[string]any, error) {
	kinds, err := traces.Kinds()
	if err != nil {
		return nil, nil, err
	}
	captures := &rpc.OpenAPISchema{Type: "object", Title: "Captures", Properties: map[string]*rpc.OpenAPISchema{}}
	captureDefaults := map[string]any{}
	for order, registration := range ArmableKinds() {
		kind := registration.Name
		plugin, err := traces.Lookup(kinds, kind)
		if err != nil {
			return nil, nil, err
		}
		params, err := plugin.Params()
		if err != nil {
			return nil, nil, fmt.Errorf("trace kind %q: %w", kind, err)
		}
		if len(registration.Seams) == 1 && slices.Contains([]target.Seam{target.Self, target.Host, target.Proxy, target.Connection}, registration.Seams[0]) {
			delete(params.Properties, "target")
		}
		if selection := params.Properties["target"]; selection != nil {
			delete(selection.Properties, "pid")
		}
		captures.Properties[kind] = captureSchema(registration.Title, order, params)
		defaults := map[string]any{enabledKey: kind == httpproxy.Kind}
		for name, property := range params.Properties {
			if property.Default != nil {
				defaults[name] = property.Default
			}
		}
		if slices.Contains(registration.Seams, target.Proxy) {
			defaults["target"] = map[string]any{"seam": target.Proxy}
			if kind == httpproxy.Kind {
				delete(defaults, "features")
			}
		}
		captureDefaults[kind] = defaults
	}
	schema := &rpc.OpenAPISchema{
		Type: "object",
		Properties: map[string]*rpc.OpenAPISchema{
			"pid":      {Type: "integer", Title: "PID", Description: "Attach to a local running process instead of a command"},
			"duration": {Type: "string", Title: "Duration", Description: "Stop after this duration, e.g. 30s"},
			"every":    {Type: "string", Title: "Checkpoint interval", Description: "Read checkpoints at this interval, e.g. 10s"},
			"command": {
				Type: "string", Title: "Command", Format: "textarea",
				Description: "Run with sh -c in the server's working directory",
				Extensions:  map[string]any{"x-clicky-order": 0},
			},
			"tree":   withOrder(treeSchema(), 1),
			"traces": withOrder(captures, 2),
		},
	}
	return schema, map[string]any{"command": "", "tree": map[string]any{"facets": false}, "traces": captureDefaults}, nil
}

// treeSchema is the run-wide process tree's options, reflected from
// traces.TreeOptions as a kind's params are.
func treeSchema() *rpc.OpenAPISchema {
	schema := rpc.SchemaForStruct(traces.TreeOptions{})
	schema.Title = "Process tree"
	schema.Description = "What the run's process tree reads of each process"
	return schema
}

// captureSchema is one armable kind's object: its enabled switch and, while
// it is on, the kind's params.
func captureSchema(title string, order int, params *rpc.OpenAPISchema) *rpc.OpenAPISchema {
	then := map[string]any{"properties": params.Properties}
	if len(params.Required) > 0 {
		then["required"] = params.Required
	}
	return &rpc.OpenAPISchema{
		Type: "object", Title: title,
		Properties: map[string]*rpc.OpenAPISchema{
			enabledKey: {Type: "boolean", Title: "Enabled", Extensions: map[string]any{"x-clicky-order": -1}},
		},
		Extensions: map[string]any{
			"x-clicky-order": order,
			"allOf": []any{map[string]any{
				"if":   map[string]any{"properties": map[string]any{enabledKey: map[string]any{"const": true}}},
				"then": then,
			}},
		},
	}
}

func withOrder(schema *rpc.OpenAPISchema, order int) *rpc.OpenAPISchema {
	if schema.Extensions == nil {
		schema.Extensions = map[string]any{}
	}
	schema.Extensions["x-clicky-order"] = order
	return schema
}
