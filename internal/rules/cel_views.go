package rules

import (
	"github.com/flanksource/telltale/pkg/tracing/groups"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"reflect"
	"strings"
)

type viewAdapter struct{ types.Adapter }

func (a *viewAdapter) NativeToValue(value any) ref.Val {
	if value == nil {
		return types.NewUnknown(1, nil)
	}
	rv := reflect.ValueOf(value)
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return types.NewUnknown(1, nil)
		}
		rv = rv.Elem()
	}
	if rv.Kind() == reflect.Slice {
		return types.NewDynamicList(a, value)
	}
	if rv.Kind() == reflect.Struct && (rv.Type().PkgPath() == reflect.TypeFor[MonitorGroup]().PkgPath() || rv.Type() == reflect.TypeFor[groups.Context]()) {
		return &viewObject{Val: a.Adapter.NativeToValue(value), adapter: a, value: rv}
	}
	return a.Adapter.NativeToValue(value)
}

type viewObject struct {
	ref.Val
	adapter *viewAdapter
	value   reflect.Value
}

func (o *viewObject) Get(name ref.Val) ref.Val {
	for _, field := range reflect.VisibleFields(o.value.Type()) {
		if field.Anonymous {
			continue
		}
		tag := field.Tag.Get("cel")
		if tag == "" {
			tag = field.Tag.Get("json")
		}
		tag, _, _ = strings.Cut(tag, ",")
		if tag == string(name.(types.String)) {
			return o.adapter.NativeToValue(o.value.FieldByIndex(field.Index).Interface())
		}
	}
	return types.NewErr("unknown field %s", name)
}
func (o *viewObject) IsSet(name ref.Val) ref.Val { return types.Bool(!types.IsUnknown(o.Get(name))) }

type viewProvider struct {
	types.Provider
	adapter *viewAdapter
}

func (p viewProvider) FindStructFieldType(object, name string) (*types.FieldType, bool) {
	field, ok := p.Provider.FindStructFieldType(object, name)
	if !ok {
		return nil, false
	}
	copyField := *field
	copyField.GetFrom = func(value any) (any, error) {
		if wrapped, ok := value.(*viewObject); ok {
			return wrapped.Get(types.String(name)), nil
		}
		wrapped := p.adapter.NativeToValue(value)
		if object, ok := wrapped.(*viewObject); ok {
			return object.Get(types.String(name)), nil
		}
		return field.GetFrom(value)
	}
	return &copyField, true
}
