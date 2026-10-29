package cli

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"

	flagmeta "github.com/flanksource/clicky/flags"
	"github.com/flanksource/clicky/rpc"
	"github.com/flanksource/telltale/pkg/tracing/target"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// paramFlag is one flag a kind's params schema binds: the param's JSON path,
// its schema and the flag's value.
type paramFlag struct {
	path   []string
	flag   *pflag.Flag
	schema *rpc.OpenAPISchema
	value  func() any
}

// paramFlags are a kind's params as flags.
type paramFlags struct {
	bound  []paramFlag
	schema *rpc.OpenAPISchema
}

type paramScope struct {
	path     []string
	required bool
}

// bindParamFlags defines a typed flag on flags for each param schema
// describes: strings, booleans, integers and string lists, an object's params
// prefixed by its name. A param of another type, or one whose flag another
// flag already defines, is an error.
func bindParamFlags(flags *pflag.FlagSet, schema *rpc.OpenAPISchema) (paramFlags, error) {
	if schema == nil {
		return paramFlags{}, fmt.Errorf("missing parameter schema")
	}
	projected := *schema
	projected.Properties = maps.Clone(schema.Properties)
	if target := projected.Properties["target"]; target != nil {
		field := targetFlagSchema(target)
		projected.Properties["target"] = &field
	}
	bound, err := bindParams(flags, &projected, paramScope{required: true})
	return paramFlags{bound: bound, schema: &projected}, err
}

func bindParams(flags *pflag.FlagSet, schema *rpc.OpenAPISchema, scope paramScope) ([]paramFlag, error) {
	defaults, err := objectDefaults(schema)
	if err != nil {
		return nil, fmt.Errorf("param %s: %w", strings.Join(scope.path, "."), err)
	}
	var bound []paramFlag
	for _, name := range slices.Sorted(maps.Keys(schema.Properties)) {
		if schema.Properties[name] == nil {
			return nil, fmt.Errorf("param %s: missing schema", name)
		}
		property, path := *schema.Properties[name], append(slices.Clone(scope.path), name)
		if value, ok := defaults[name]; ok {
			property.Default = value
		}
		required := scope.required && slices.Contains(schema.Required, name)
		if property.Type == "object" && property.AdditionalProperties == nil && property.Format != "json" {
			nested, err := bindParams(flags, &property, paramScope{path: path, required: required || (scope.required && property.Default != nil)})
			if err != nil {
				return nil, err
			}
			bound = append(bound, nested...)
			continue
		}
		param, err := bindParam(flags, &property, path)
		if err != nil {
			return nil, err
		}
		if required && property.Default == nil {
			if err := cobra.MarkFlagRequired(flags, param.flag.Name); err != nil {
				return nil, err
			}
		}
		bound = append(bound, param)
	}
	return bound, nil
}

func targetFlagSchema(schema *rpc.OpenAPISchema) rpc.OpenAPISchema {
	projected := *schema
	projected.Properties = maps.Clone(schema.Properties)
	seams, _ := schema.Extensions["x-telltale-seams"].([]target.Seam)
	if slices.Equal(seams, []target.Seam{target.Connection}) {
		delete(projected.Properties, "pid")
	}
	if !slices.Contains(seams, target.Agent) {
		for _, field := range []string{"agent", "selector", "wait"} {
			delete(projected.Properties, field)
		}
	}
	if !slices.Contains(seams, target.Agent) && !slices.Contains(seams, target.Self) {
		delete(projected.Properties, "allowMutation")
	}
	if selector := projected.Properties["selector"]; selector != nil {
		field := *selector
		field.Type = "string"
		projected.Properties["selector"] = &field
	}
	return projected
}

func bindParam(flags *pflag.FlagSet, schema *rpc.OpenAPISchema, path []string) (paramFlag, error) {
	name := flagName(path)
	if flags.Lookup(name) != nil {
		return paramFlag{}, fmt.Errorf("param %s: its flag --%s is already defined", strings.Join(path, "."), name)
	}
	param := paramFlag{path: path, schema: schema}
	var err error
	param.value, err = bindParamValue(flags, schema, name)
	if err != nil {
		return paramFlag{}, fmt.Errorf("param %s: %w", strings.Join(path, "."), err)
	}
	param.flag = flags.Lookup(name)
	flagmeta.SetCategory(param.flag, paramsCategory)
	flagmeta.SetAllowedValues(param.flag, paramChoices(schema)...)
	if schema.Default != nil {
		if _, err := param.read(); err != nil {
			return paramFlag{}, err
		}
	}
	return param, nil
}

func bindParamValue(flags *pflag.FlagSet, schema *rpc.OpenAPISchema, name string) (func() any, error) {
	usage := paramUsage(schema)
	switch {
	case schema.Type == "string":
		return bindTypedParam(schema, func(value string) *string { return flags.String(name, value, usage) })
	case schema.Type == "boolean":
		return bindTypedParam(schema, func(value bool) *bool { return flags.Bool(name, value, usage) })
	case schema.Type == "integer":
		return bindTypedParam(schema, func(value int) *int { return flags.Int(name, value, usage) })
	case schema.Type == "number":
		return bindTypedParam(schema, func(value float64) *float64 { return flags.Float64(name, value, usage) })
	case schema.Type == "array" && schema.Items != nil && schema.Items.Type == "string":
		return bindTypedParam(schema, func(value []string) *[]string { return flags.StringSlice(name, value, usage) })
	case schema.Type == "object" && schema.AdditionalProperties != nil && schema.AdditionalProperties.Type == "string":
		return bindMapParam(flags, schema, name)
	case schema.Type == "object" && (schema.AdditionalProperties != nil || schema.Format == "json"):
		return bindJSONParam(flags, schema, name)
	default:
		return nil, fmt.Errorf("a %s param cannot be a flag", describeType(schema))
	}
}

// paramsCategory is the help section a kind's param flags are listed under.
const paramsCategory = "Trace params"

// flagName is a param's JSON path in kebab case: environment.keys is
// environment-keys, upstreamCA is upstream-ca.
func flagName(path []string) string {
	if len(path) == 2 && path[0] == "target" {
		if path[1] == "allowMutation" {
			return "allow-mutation"
		}
		return path[1]
	}
	parts := make([]string, 0, len(path))
	for _, name := range path {
		var kebab strings.Builder
		runes := []rune(name)
		for i, r := range runes {
			lowerBefore := i > 0 && (unicode.IsLower(runes[i-1]) || unicode.IsDigit(runes[i-1]))
			acronymEnds := i > 0 && unicode.IsUpper(runes[i-1]) && i+1 < len(runes) && unicode.IsLower(runes[i+1])
			if unicode.IsUpper(r) && (lowerBefore || acronymEnds) {
				kebab.WriteByte('-')
			}
			kebab.WriteRune(unicode.ToLower(r))
		}
		parts = append(parts, kebab.String())
	}
	return strings.Join(parts, "-")
}

// enumOf is a param's choices, which a schema keeps as enum or, for a type
// describing itself (JSONSchema), as an extension.
func enumOf(schema *rpc.OpenAPISchema) []string {
	values := schema.Enum
	if len(values) == 0 {
		switch extension := schema.Extensions["enum"].(type) {
		case []any:
			values = extension
		case []string:
			return slices.Clone(extension)
		}
	}
	choices := make([]string, 0, len(values))
	for _, value := range values {
		choices = append(choices, fmt.Sprint(value))
	}
	return choices
}

func paramUsage(schema *rpc.OpenAPISchema) string {
	usage := schema.Description
	if usage == "" {
		usage = schema.Title
	}
	if choices := paramChoices(schema); len(choices) > 0 {
		usage += " (one of " + strings.Join(choices, ", ") + ")"
	}
	return usage
}

func bindTypedParam[T any](schema *rpc.OpenAPISchema, bind func(T) *T) (func() any, error) {
	initial, err := paramDefault[T](schema)
	if err != nil {
		return nil, err
	}
	value := bind(initial)
	return func() any { return *value }, nil
}

func paramChoices(schema *rpc.OpenAPISchema) []string {
	if schema.Type == "array" && schema.Items != nil {
		return enumOf(schema.Items)
	}
	return enumOf(schema)
}

func describeType(schema *rpc.OpenAPISchema) string {
	if schema.Type == "array" && schema.Items != nil {
		return "list of " + describeType(schema.Items)
	}
	if schema.Type == "" {
		return "untyped"
	}
	return schema.Type
}

// params are the params the flags set, nested by their JSON paths; nil when
// no flag was set, so the kind's defaults apply. A value outside a param's
// choices is refused, naming its flag.
func (p paramFlags) params() (json.RawMessage, error) {
	values := map[string]any{}
	for _, param := range p.bound {
		if !param.flag.Changed {
			continue
		}
		if len(param.path) == 2 && param.path[0] == "target" && param.path[1] == "pid" {
			continue
		}
		value, err := param.read()
		if err != nil {
			return nil, err
		}
		object := values
		for _, name := range param.path[:len(param.path)-1] {
			nested, ok := object[name].(map[string]any)
			if !ok {
				nested = map[string]any{}
				object[name] = nested
			}
			object = nested
		}
		object[param.path[len(param.path)-1]] = value
	}
	if err := validateRequiredParams(p.schema, values, nil); err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return nil, nil
	}
	return json.Marshal(values)
}

func (p paramFlag) read() (any, error) {
	value := p.value()
	if p.schema.Type == "object" && (p.schema.AdditionalProperties != nil || p.schema.Format == "json") {
		if p.schema.AdditionalProperties != nil && p.schema.AdditionalProperties.Type == "string" {
			return readMapParam(value.([]string), p.flag.Name)
		}
		return readJSONParam(value.(string), p.flag.Name)
	}
	if p.flag.Name == "selector" && len(p.path) == 2 && p.path[0] == "target" {
		selector, err := target.ParseSelector(value.(string))
		if err != nil {
			return nil, fmt.Errorf("--selector: %w", err)
		}
		return selector, nil
	}
	if choices := paramChoices(p.schema); len(choices) > 0 {
		values := []string{fmt.Sprint(value)}
		if list, ok := value.([]string); ok {
			values = list
		}
		for _, item := range values {
			if !slices.Contains(choices, item) {
				return nil, fmt.Errorf("--%s: %q is not one of %s", p.flag.Name, item, strings.Join(choices, ", "))
			}
		}
	}
	return value, nil
}
