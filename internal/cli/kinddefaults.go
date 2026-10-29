package cli

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/flanksource/clicky/rpc"
	"github.com/spf13/pflag"
)

func paramDefault[T any](schema *rpc.OpenAPISchema) (T, error) {
	var initial T
	if schema.Default == nil {
		return initial, nil
	}
	raw, err := json.Marshal(schema.Default)
	if err != nil {
		return initial, fmt.Errorf("encode default: %w", err)
	}
	if err := json.Unmarshal(raw, &initial); err != nil {
		return initial, fmt.Errorf("invalid %s default: %w", describeType(schema), err)
	}
	return initial, nil
}

func bindMapParam(flags *pflag.FlagSet, schema *rpc.OpenAPISchema, name string) (func() any, error) {
	defaults, err := paramDefault[map[string]string](schema)
	if err != nil {
		return nil, err
	}
	var assignments []string
	for _, key := range slices.Sorted(maps.Keys(defaults)) {
		assignments = append(assignments, key+"="+defaults[key])
	}
	values := flags.StringSlice(name, assignments, paramUsage(schema)+" (key=value pairs)")
	return func() any { return *values }, nil
}

func readMapParam(assignments []string, flag string) (map[string]string, error) {
	values := map[string]string{}
	for _, assignment := range assignments {
		key, value, ok := strings.Cut(assignment, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("--%s: %q must be key=value", flag, assignment)
		}
		if _, exists := values[key]; exists {
			return nil, fmt.Errorf("--%s: key %q is repeated", flag, key)
		}
		values[key] = value
	}
	return values, nil
}

func bindJSONParam(flags *pflag.FlagSet, schema *rpc.OpenAPISchema, name string) (func() any, error) {
	initial := ""
	if schema.Default != nil {
		raw, err := json.Marshal(schema.Default)
		if err != nil {
			return nil, fmt.Errorf("encode object default: %w", err)
		}
		initial = string(raw)
	}
	value := flags.String(name, initial, paramUsage(schema)+" (JSON object)")
	return func() any { return *value }, nil
}

func readJSONParam(raw, flag string) (map[string]json.RawMessage, error) {
	var value map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return nil, fmt.Errorf("--%s: expected a JSON object: %w", flag, err)
	}
	if value == nil {
		return nil, fmt.Errorf("--%s: expected a JSON object", flag)
	}
	return value, nil
}
