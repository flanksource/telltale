package rules

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/common/types/traits"
	"github.com/google/cel-go/ext"
)

type expression struct {
	program        cel.Program
	source, result string
}

func compileExpression(source, result string, targeting bool) (*expression, error) {
	env, err := cel.NewEnv(ext.NativeTypes(reflect.TypeFor[MonitorGroup](), reflect.TypeFor[Host](), reflect.TypeFor[Incident](), ext.ParseStructTag("json")), ext.Lists(ext.ListsVersion(3)), ext.Bindings(),
		cel.Variable("group", cel.ObjectType("rules.MonitorGroup")), cel.Variable("host", cel.ObjectType("rules.Host")), cel.Variable("now", cel.TimestampType),
		cel.Variable("cores", cel.DoubleType), cel.Variable("cpu", cel.DoubleType), cel.Variable("rss", cel.IntType), cel.Variable("mem", cel.DoubleType), cel.CrossTypeNumericComparisons(true))
	if err != nil {
		return nil, err
	}
	adapter := &viewAdapter{Adapter: env.CELTypeAdapter()}
	options := []cel.EnvOption{cel.CustomTypeAdapter(adapter), cel.CustomTypeProvider(viewProvider{Provider: env.CELTypeProvider(), adapter: adapter})}
	if targeting {
		options = append(options, cel.Variable("incident", cel.ObjectType("rules.Incident")))
	}
	env, err = env.Extend(options...)
	if err != nil {
		return nil, err
	}
	ast, issues := env.Compile(source)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("CEL %q: %w", source, issues.Err())
	}
	expected := cel.BoolType
	if result == "process" {
		expected = cel.ListType(cel.ObjectType("rules.ProcessRef"))
	}
	if result == "session" {
		expected = cel.ListType(cel.ObjectType("rules.SessionRef"))
	}
	output := ast.OutputType()
	if result == "either" {
		if output.IsExactType(cel.ListType(cel.ObjectType("rules.ProcessRef"))) {
			result = "process"
		} else if output.IsExactType(cel.ListType(cel.ObjectType("rules.SessionRef"))) {
			result = "session"
		} else {
			return nil, fmt.Errorf("CEL targets must return a process or session reference list, got %s", output)
		}
	} else if !output.IsExactType(expected) {
		return nil, fmt.Errorf("CEL %q: expected %s, got %s", source, expected, output)
	}
	program, err := env.Program(ast, cel.CostLimit(1_000_000), cel.InterruptCheckFrequency(100))
	if err != nil {
		return nil, err
	}
	return &expression{program: program, source: source, result: result}, nil
}

func (e *expression) eval(ctx context.Context, g MonitorGroup, h Host, at time.Time, incident Incident) (ref.Val, error) {
	ctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	vars := map[string]any{"group": g, "host": h, "now": at, "incident": incident, "rss": g.RSS, "mem": g.Mem, "cores": metricValue(g.Cores), "cpu": metricValue(g.CPU)}
	value, _, err := e.program.ContextEval(ctx, vars)
	if err != nil {
		return nil, fmt.Errorf("CEL %q: %w", e.source, err)
	}
	return value, nil
}
func metricValue(value *float64) ref.Val {
	if value == nil {
		return types.NewUnknown(1, nil)
	}
	return types.Double(*value)
}
func (e *expression) predicate(ctx context.Context, g MonitorGroup, h Host, at time.Time, i Incident) (bool, bool, error) {
	value, err := e.eval(ctx, g, h, at, i)
	if err != nil {
		return false, false, err
	}
	if types.IsUnknown(value) {
		return false, false, nil
	}
	boolean, ok := value.(types.Bool)
	if !ok {
		return false, false, fmt.Errorf("CEL %q: expected bool, got %s", e.source, value.Type())
	}
	return bool(boolean), true, nil
}
func (e *expression) targets(ctx context.Context, g MonitorGroup, h Host, at time.Time, i Incident) (Targets, error) {
	value, err := e.eval(ctx, g, h, at, i)
	if err != nil {
		return Targets{}, err
	}
	if types.IsUnknown(value) {
		return Targets{}, fmt.Errorf("CEL %q: unknown target measurements", e.source)
	}
	list, ok := value.(traits.Lister)
	if !ok {
		return Targets{}, fmt.Errorf("CEL targets: expected list")
	}
	result := Targets{}
	seen := map[string]bool{}
	for index := types.Int(0); index < list.Size().(types.Int); index++ {
		item := list.Get(index)
		if e.result == "process" {
			converted, err := item.ConvertToNative(reflect.TypeFor[ProcessRef]())
			if err != nil {
				return Targets{}, err
			}
			p := converted.(ProcessRef)
			found := false
			for _, candidate := range g.Members {
				if reflect.DeepEqual(candidate, p) {
					found = true
					break
				}
			}
			if !found {
				return Targets{}, fmt.Errorf("process target %q is outside group or was forged", p.Key)
			}
			if !seen[p.Key] {
				result.Processes = append(result.Processes, p)
				seen[p.Key] = true
			}
		} else {
			converted, err := item.ConvertToNative(reflect.TypeFor[SessionRef]())
			if err != nil {
				return Targets{}, err
			}
			s := converted.(SessionRef)
			found := false
			for _, candidate := range g.Sessions {
				if reflect.DeepEqual(candidate, s) {
					found = true
					break
				}
			}
			if !found {
				return Targets{}, fmt.Errorf("session target %q is outside group or was forged", s.Key)
			}
			if !seen[s.Key] {
				result.Sessions = append(result.Sessions, s)
				seen[s.Key] = true
			}
		}
	}
	return result, nil
}
