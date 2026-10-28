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
type paramFlags []paramFlag

// bindParamFlags defines a typed flag on flags for each param schema
// describes: strings, booleans, integers and string lists, an object's params
// prefixed by its name. A param of another type, or one whose flag another
// flag already defines, is an error.
func bindParamFlags(flags *pflag.FlagSet, schema *rpc.OpenAPISchema) (paramFlags, error) {
	return bindParams(flags, schema, nil)
}

func bindParams(flags *pflag.FlagSet, schema *rpc.OpenAPISchema, prefix []string) (paramFlags, error) {
	var bound paramFlags
	for _, name := range slices.Sorted(maps.Keys(schema.Properties)) {
		property, path := schema.Properties[name], append(slices.Clone(prefix), name)
		if len(prefix) == 0 && name == "target" {
			seams, _ := property.Extensions["x-telltale-seams"].([]target.Seam)
			for _, field := range []string{"seam", "pid", "agent", "selector", "wait", "allowMutation"} {
				if (field == "agent" || field == "selector" || field == "wait") && !slices.Contains(seams, target.Agent) {
					continue
				}
				if field == "allowMutation" && !slices.Contains(seams, target.Agent) && !slices.Contains(seams, target.Self) {
					continue
				}
				fieldSchema := *property.Properties[field]
				if field == "selector" {
					fieldSchema.Type = "string"
				}
				param, err := bindParam(flags, &fieldSchema, []string{"target", field})
				if err != nil {
					return nil, err
				}
				bound = append(bound, param)
			}
			continue
		}
		if property.Type == "object" {
			nested, err := bindParams(flags, property, path)
			if err != nil {
				return nil, err
			}
			bound = append(bound, nested...)
			continue
		}
		param, err := bindParam(flags, property, path)
		if err != nil {
			return nil, err
		}
		bound = append(bound, param)
	}
	return bound, nil
}

func bindParam(flags *pflag.FlagSet, schema *rpc.OpenAPISchema, path []string) (paramFlag, error) {
	name := flagName(path)
	if flags.Lookup(name) != nil {
		return paramFlag{}, fmt.Errorf("param %s: its flag --%s is already defined", strings.Join(path, "."), name)
	}
	usage := paramUsage(schema)
	param := paramFlag{path: path, schema: schema}
	switch {
	case schema.Type == "string":
		value := flags.String(name, defaultOf(schema, ""), usage)
		param.value = func() any { return *value }
	case schema.Type == "boolean":
		value := flags.Bool(name, defaultOf(schema, false), usage)
		param.value = func() any { return *value }
	case schema.Type == "integer":
		value := flags.Int(name, int(defaultOf(schema, float64(0))), usage)
		param.value = func() any { return *value }
	case schema.Type == "array" && schema.Items != nil && schema.Items.Type == "string":
		value := flags.StringSlice(name, nil, usage)
		param.value = func() any { return *value }
	default:
		return paramFlag{}, fmt.Errorf("param %s: a %s param cannot be a flag", strings.Join(path, "."), describeType(schema))
	}
	param.flag = flags.Lookup(name)
	flagmeta.SetCategory(param.flag, paramsCategory)
	return param, nil
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
	if extension, ok := schema.Extensions["enum"].([]any); ok && len(values) == 0 {
		values = extension
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
	if choices := enumOf(schema); len(choices) > 0 {
		usage += " (one of " + strings.Join(choices, ", ") + ")"
	}
	return usage
}

func defaultOf[T any](schema *rpc.OpenAPISchema, zero T) T {
	if value, ok := schema.Default.(T); ok {
		return value
	}
	return zero
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
	for _, param := range p {
		if !param.flag.Changed {
			continue
		}
		if len(param.path) == 2 && param.path[0] == "target" && param.path[1] == "pid" {
			continue
		}
		value := param.value()
		if param.flag.Name == "selector" && len(param.path) == 2 && param.path[0] == "target" {
			selector, err := target.ParseSelector(value.(string))
			if err != nil {
				return nil, fmt.Errorf("--selector: %w", err)
			}
			value = selector
		}
		if choices := enumOf(param.schema); len(choices) > 0 && !slices.Contains(choices, fmt.Sprint(value)) {
			return nil, fmt.Errorf("--%s: %q is not one of %s", param.flag.Name, value, strings.Join(choices, ", "))
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
	if len(values) == 0 {
		return nil, nil
	}
	return json.Marshal(values)
}
