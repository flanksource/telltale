package telltale

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/flanksource/clicky/rpc"
	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/tracing/httpproxy"
)

// ArmableKinds are the registered kinds the start form offers
// (traces.Registration.Armable): the proxies a command is pointed at through
// their exports, sorted by name.
func ArmableKinds() []traces.Registration {
	var armable []traces.Registration
	for _, registration := range traces.Registrations() {
		if registration.Armable {
			armable = append(armable, registration)
		}
	}
	return armable
}

// enabledKey is the switch each capture's object in a StartRequest carries
// beside its kind's params.
const enabledKey = "enabled"

// StartRequest is a run as a client asks for it, the shape StartSchema
// describes: a shell command and, per armable kind, whether to arm it and
// its params.
type StartRequest struct {
	Command string                     `json:"command"`
	Facets  bool                       `json:"facets,omitempty"`
	Traces  map[string]json.RawMessage `json:"traces,omitempty"`
}

// FieldError is one refused field of a StartRequest, at its JSON pointer.
type FieldError struct {
	InstancePath string `json:"instancePath"`
	Message      string `json:"message"`
}

// ValidationError is a StartRequest refused field by field.
type ValidationError struct {
	Fields []FieldError `json:"errors"`
}

func (e *ValidationError) Error() string {
	messages := make([]string, 0, len(e.Fields))
	for _, field := range e.Fields {
		messages = append(messages, field.InstancePath+": "+field.Message)
	}
	return "invalid start request: " + strings.Join(messages, "; ")
}

// Options are the StartOptions the request asks for, its command run with
// `sh -c` in workDir and traced from its own root (TraceRootCommand). A
// refused request is a *ValidationError naming each field.
func (r StartRequest) Options(workDir string) (StartOptions, error) {
	kinds, err := traces.Kinds()
	if err != nil {
		return StartOptions{}, err
	}
	var fields []FieldError
	if strings.TrimSpace(r.Command) == "" {
		fields = append(fields, FieldError{InstancePath: "/command", Message: "a command is required"})
	}
	var specs []TraceSpec
	for _, kind := range slices.Sorted(maps.Keys(r.Traces)) {
		spec, armed, err := armedSpec(kind, r.Traces[kind])
		if err == nil && armed {
			err = traces.Validate(kinds, kind, spec.Params)
		}
		if err != nil {
			fields = append(fields, FieldError{InstancePath: "/traces/" + kind, Message: err.Error()})
			continue
		}
		if armed {
			specs = append(specs, spec)
		}
	}
	if len(fields) > 0 {
		return StartOptions{}, &ValidationError{Fields: fields}
	}
	return StartOptions{
		WorkDir: workDir, Dir: workDir, Command: []string{"sh", "-c", r.Command}, Traces: specs, Facets: r.Facets,
		TraceRoot: TraceRootCommand,
	}, nil
}

// armedSpec reads one capture's object: its enabled switch and, the switch
// stripped, its kind's params.
func armedSpec(kind string, raw json.RawMessage) (TraceSpec, bool, error) {
	var names []string
	for _, registration := range ArmableKinds() {
		names = append(names, registration.Name)
	}
	if !slices.Contains(names, kind) {
		return TraceSpec{}, false, fmt.Errorf("%q is not a kind a run arms (want one of %s)", kind, strings.Join(names, ", "))
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return TraceSpec{}, false, fmt.Errorf("want an object: %w", err)
	}
	var enabled bool
	if value, ok := values[enabledKey]; ok {
		if err := json.Unmarshal(value, &enabled); err != nil {
			return TraceSpec{}, false, fmt.Errorf("%s: want a boolean: %w", enabledKey, err)
		}
	}
	delete(values, enabledKey)
	params, err := json.Marshal(values)
	if err != nil {
		return TraceSpec{}, false, fmt.Errorf("encode params: %w", err)
	}
	return TraceSpec{Kind: kind, Params: params}, enabled, nil
}

// StartSchema is the JSON schema of a StartRequest, the form a client renders
// to start a run, and the values it starts from: each armable kind's params
// shown once the kind is enabled, http_proxy enabled as `trace run` arms it.
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
		captures.Properties[kind] = captureSchema(registration.Title, order, params)
		defaults := map[string]any{enabledKey: kind == httpproxy.Kind}
		for name, property := range params.Properties {
			if property.Default != nil {
				defaults[name] = property.Default
			}
		}
		captureDefaults[kind] = defaults
	}
	schema := &rpc.OpenAPISchema{
		Type: "object", Required: []string{"command"},
		Properties: map[string]*rpc.OpenAPISchema{
			"command": {
				Type: "string", Title: "Command", Format: "textarea",
				Description: "Run with sh -c in the server's work dir",
				Extensions:  map[string]any{"x-clicky-order": 0},
			},
			"facets": {
				Type: "boolean", Title: "Process facets",
				Description: "Read every process's open files, sockets and environment into the process tree",
				Extensions:  map[string]any{"x-clicky-order": 1},
			},
			"traces": withOrder(captures, 2),
		},
	}
	return schema, map[string]any{"command": "", "facets": false, "traces": captureDefaults}, nil
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
