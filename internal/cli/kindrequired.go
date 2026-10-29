package cli

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/flanksource/clicky/rpc"
)

func objectDefaults(schema *rpc.OpenAPISchema) (map[string]any, error) {
	if schema == nil || schema.Type != "object" {
		return nil, fmt.Errorf("expected an object schema")
	}
	for _, name := range schema.Required {
		if schema.Properties[name] == nil {
			return nil, fmt.Errorf("required property %q has no schema", name)
		}
	}
	defaults := map[string]any{}
	if schema.Default != nil {
		raw, err := json.Marshal(schema.Default)
		if err != nil {
			return nil, fmt.Errorf("encode object default: %w", err)
		}
		if err := json.Unmarshal(raw, &defaults); err != nil {
			return nil, fmt.Errorf("invalid object default: %w", err)
		}
	}
	return defaults, nil
}

func validateRequiredParams(schema *rpc.OpenAPISchema, values map[string]any, prefix []string) error {
	defaults, err := objectDefaults(schema)
	if err != nil {
		return err
	}
	for _, name := range slices.Sorted(maps.Keys(schema.Properties)) {
		property := schema.Properties[name]
		value, given := values[name]
		if !given {
			value, given = defaults[name]
		}
		if !given && property.Default != nil {
			value, given = property.Default, true
		}
		path := append(slices.Clone(prefix), name)
		if !given && slices.Contains(schema.Required, name) {
			return fmt.Errorf("--%s is required", flagName(path))
		}
		if property.Type == "object" && property.AdditionalProperties == nil && property.Format != "json" && given {
			nested, ok := value.(map[string]any)
			if !ok {
				return fmt.Errorf("param %s: expected an object", strings.Join(path, "."))
			}
			if err := validateRequiredParams(property, nested, path); err != nil {
				return err
			}
		}
	}
	return nil
}
