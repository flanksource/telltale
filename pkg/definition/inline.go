package definition

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

// InlineName is the name of the definition an ad-hoc start request's
// captures compile as, the prefix of their entries' default names.
const InlineName = "adhoc"

// inlined is an entry whose fields are not its kind's params by name: once
// the params are decoded into it, it moves them to its own fields.
type inlined interface {
	inline(params map[string]any) error
}

// Inline is a start request's ad-hoc captures as a definition, so they
// compile as a stored definition's entries do: each in the one typed list of
// its kind and seam whose entry carries every param it sets. Captures maps
// each entry's pointer to its capture's, /traces/<kind>.
func Inline(captures []telltale.RequestedCapture) (*TraceDefinition, map[string]string, []telltale.FieldError) {
	def := &TraceDefinition{
		TypeMeta:   metav1.TypeMeta{APIVersion: APIVersion, Kind: Kind},
		ObjectMeta: metav1.ObjectMeta{Name: InlineName},
	}
	pointers := map[string]string{}
	var fields []telltale.FieldError
	for _, capture := range captures {
		at, err := placeCapture(&def.Spec, capture)
		if err != nil {
			fields = append(fields, telltale.FieldError{InstancePath: "/traces/" + capture.Kind, Message: err.Error()})
			continue
		}
		pointers[at.Pointer()] = "/traces/" + capture.Kind
	}
	return def, pointers, fields
}

// placeCapture appends capture's entry to the list of spec that carries it.
func placeCapture(spec *Spec, capture telltale.RequestedCapture) (Located, error) {
	if capture.Target.PID != 0 {
		return Located{}, errors.New("target.pid: a capture is aimed at the run's own pid; set the start request's pid")
	}
	var matches []Located
	var tried []string
	var lastErr error
	for _, list := range Lists() {
		if list.Kind != capture.Kind || !aimable(list, capture.Seam) {
			continue
		}
		tried = append(tried, list.Path())
		at, err := inlineEntry(list, capture)
		if err != nil {
			lastErr = err
			continue
		}
		matches = append(matches, at)
	}
	switch {
	case len(tried) == 0:
		return Located{}, unarmable(capture)
	case len(matches) == 0 && len(tried) == 1:
		return Located{}, lastErr
	case len(matches) == 0:
		return Located{}, fmt.Errorf("none of %s carries these params (%v): start a trace definition instead", strings.Join(tried, ", "), lastErr)
	case len(matches) > 1:
		paths := make([]string, 0, len(matches))
		for _, match := range matches {
			paths = append(paths, match.Path())
		}
		return Located{}, fmt.Errorf("%s each carry these params: start a trace definition that names one", strings.Join(paths, ", "))
	}
	appendEntry(spec, matches[0])
	return matches[0], nil
}

// unarmable is why no list takes capture: a seam its kind does not support,
// or one no definition list arms it at.
func unarmable(capture telltale.RequestedCapture) error {
	registration, err := traces.Registered(capture.Kind)
	if err != nil {
		return err
	}
	if !slices.Contains(registration.Seams, capture.Seam) {
		return fmt.Errorf("%w %q for %s; supported seams: %v", target.ErrUnsupportedSeam, capture.Seam, capture.Kind, registration.Seams)
	}
	return fmt.Errorf("no trace definition list arms %s at the %s seam", capture.Kind, capture.Seam)
}

// aimable reports whether list's entries can be aimed at seam: its own, or an
// agent's for a process list whose entries may name one.
func aimable(list Located, seam target.Seam) bool {
	if list.Seam == seam {
		return true
	}
	_, agent := list.Entry.(agentEntry)
	return seam == target.Agent && list.Seam == target.Process && agent
}

// inlineEntry is capture decoded into a new entry of list, refused unless the
// entry compiles to every param the capture sets.
func inlineEntry(list Located, capture telltale.RequestedCapture) (Located, error) {
	entry := reflect.New(reflect.TypeOf(list.Entry).Elem()).Interface().(Entry)
	params := maps.Clone(capture.Params)
	if capture.Seam == target.Agent {
		params["agent"] = AgentRef{ID: capture.Target.Agent, Selector: capture.Target.Selector, Wait: capture.Target.Wait}
	}
	data, err := json.Marshal(params)
	if err == nil {
		err = json.Unmarshal(data, entry)
	}
	if moved, ok := entry.(inlined); ok && err == nil {
		err = moved.inline(params)
	}
	if err != nil {
		return Located{}, err
	}
	at := list
	at.Entry, at.Index, at.Count, at.Name = entry, 0, 1, ""
	options, _, err := entry.options(at)
	if err != nil {
		return Located{}, err
	}
	compiled := map[string]any{}
	if data, err = json.Marshal(options); err == nil {
		err = json.Unmarshal(data, &compiled)
	}
	if err != nil {
		return Located{}, err
	}
	for _, key := range slices.Sorted(maps.Keys(capture.Params)) {
		if !carries(capture.Params[key], compiled[key]) {
			return Located{}, fmt.Errorf("%s does not carry %s", list.Path(), key)
		}
	}
	return at, nil
}

// carries reports whether got holds every set value of want.
func carries(want, got any) bool {
	if isZero(want) {
		return true
	}
	wanted, isMap := want.(map[string]any)
	if !isMap {
		return reflect.DeepEqual(want, got)
	}
	held, ok := got.(map[string]any)
	if !ok {
		return false
	}
	for key, value := range wanted {
		if !carries(value, held[key]) {
			return false
		}
	}
	return true
}

// appendEntry appends at's entry to its list in spec.
func appendEntry(spec *Spec, at Located) {
	list := fieldByJSON(reflect.ValueOf(spec).Elem(), at.Section)
	if at.Group != "" {
		list = fieldByJSON(list, at.Group)
	}
	list = fieldByJSON(list, at.Type)
	list.Set(reflect.Append(list, reflect.ValueOf(at.Entry).Elem()))
}

func fieldByJSON(value reflect.Value, name string) reflect.Value {
	for i := range value.NumField() {
		if jsonName(value.Type().Field(i)) == name {
			return value.Field(i)
		}
	}
	panic(fmt.Sprintf("trace definition: %s has no field %q", value.Type(), name))
}

// inline takes the exec kind's name glob, which decodes as the entry's name.
func (p *ExecProbe) inline(map[string]any) error {
	p.Executable, p.Name = p.Name, ""
	return nil
}

// inline takes the spans kind's opensearch block.
func (s *OpenSearchSpans) inline(params map[string]any) error {
	data, err := json.Marshal(params["opensearch"])
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &s.Params)
}
