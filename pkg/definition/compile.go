package definition

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

// The labels each capture's session carries, tying it back to its entry.
const (
	LabelEntry      = "telltale_entry"
	LabelDefinition = "telltale_definition"
)

// Validate refuses what a run of def would refuse that can be told without
// starting it: names, connections, each entry's options as its kind decodes
// them, and proxies that would bind one port or export one variable.
func Validate(def *TraceDefinition, options Options) error {
	_, refusals := compile(def, options)
	return refusals.err()
}

// Specs compiles def to the captures a run arms, each aimed at the seam its
// group implies and labelled with its entry and definition.
func Specs(def *TraceDefinition, options Options) ([]telltale.TraceSpec, error) {
	specs, refusals := compile(def, options)
	if err := refusals.err(); err != nil {
		return nil, err
	}
	return specs, nil
}

func compile(def *TraceDefinition, options Options) ([]telltale.TraceSpec, *ValidationError) {
	resolved, refusals := resolve(def, options)
	if resolved == nil {
		return nil, refusals
	}
	kinds, err := traces.Kinds()
	if err != nil {
		refusals.add("", err)
		return nil, refusals
	}
	specs := make([]telltale.TraceSpec, 0, len(resolved.Entries))
	for _, at := range resolved.Entries {
		spec, err := compileRefusedConnection(kinds, at, def.Name, refusals.refuses(at.Pointer()+"/connection"))
		refusals.add(at.Pointer(), err)
		specs = append(specs, spec)
	}
	checkProxies(resolved.Entries, refusals)
	return specs, refusals
}

// connectionStandIn stands in for the connection of an entry already refused
// for naming none, so the kind judges the rest of the entry.
const connectionStandIn = "connection://telltale-validation"

// compileRefusedConnection is compileEntry, judged with a stand-in
// connection when the entry's own was refused: the kind would only repeat
// that it has none, and would hide what else the entry gets wrong.
func compileRefusedConnection(kinds *tracing.Kinds, at Located, definition string, refused bool) (telltale.TraceSpec, error) {
	entry, ok := at.Entry.(connectionEntry)
	if !refused || !ok {
		return compileEntry(kinds, at, definition)
	}
	slot := entry.connection(at)
	original := *slot.field
	*slot.field = connectionStandIn
	defer func() { *slot.field = original }()
	return compileEntry(kinds, at, definition)
}

// compileEntry is at's capture: its kind's params, checked as the kind
// decodes them, and its checkpoint params.
func compileEntry(kinds *tracing.Kinds, at Located, definition string) (telltale.TraceSpec, error) {
	spec := telltale.TraceSpec{Kind: at.Kind, Labels: map[string]string{LabelEntry: at.Name, LabelDefinition: definition}}
	options, checkpoint, err := at.Entry.options(at)
	if err != nil {
		return spec, err
	}
	if err := checkCredentials(at.Entry); err != nil {
		return spec, err
	}
	aimed, err := aim(at)
	if err != nil {
		return spec, err
	}
	if spec.Params, err = withTarget(options, aimed); err != nil {
		return spec, err
	}
	if checkpoint != nil {
		if spec.Checkpoint, err = json.Marshal(checkpoint); err != nil {
			return spec, err
		}
	}
	if _, registered := kinds.Get(at.Kind); !registered {
		err := fmt.Errorf("compiles to trace kind %q, which telltale does not register (registered: %v)", at.Kind, traces.KindNames(kinds))
		if validator, ok := options.(tracing.Validator); ok {
			err = errors.Join(err, validator.Validate())
		}
		return spec, err
	}
	params, err := withExecution(spec.Params, aimed)
	if err != nil {
		return spec, err
	}
	return spec, traces.Validate(kinds, at.Kind, params)
}

// withExecution is params with stand-ins for what only a run knows, so a
// static check judges the options and never the moment it runs: a capture
// of the execution's process gets this process's pid in place of the one the
// run gives it (aimedSpecs), and one through an agent a selector it waits
// for, as no agent need be connected while a definition is saved.
func withExecution(params json.RawMessage, aimed target.Target) (json.RawMessage, error) {
	switch aimed.Seam {
	case target.Agent:
		aimed.PID, aimed.Agent, aimed.Selector, aimed.Wait = 0, "", map[string]string{"telltale.validation": "static"}, true
	case target.Process:
		aimed.PID = os.Getpid()
	default:
		return params, nil
	}
	values := map[string]any{}
	if err := json.Unmarshal(params, &values); err != nil {
		return nil, err
	}
	values["target"] = aimed
	return json.Marshal(values)
}

// withTarget is options with the target added: a kind's params are its
// options and its target, flattened. An option an entry leaves at its zero
// value is left out, so the kind's own default applies, as it does to a
// field a start request omits.
func withTarget(options any, aimed target.Target) (json.RawMessage, error) {
	data, err := json.Marshal(options)
	if err != nil {
		return nil, err
	}
	values := map[string]any{}
	if err := json.Unmarshal(data, &values); err != nil {
		return nil, fmt.Errorf("options are not an object: %w", err)
	}
	for key, value := range values {
		if isZero(value) {
			delete(values, key)
		}
	}
	values["target"] = aimed
	return json.Marshal(values)
}

func isZero(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return typed == ""
	case float64:
		return typed == 0
	case bool:
		return !typed
	case []any:
		return len(typed) == 0
	case map[string]any:
		return len(typed) == 0
	}
	return false
}

// credentialURLs are the entries that carry a URL or DSN of their own.
type credentialURLs interface {
	urls() map[string]string
}

func (p *ProxyPostgres) urls() map[string]string { return map[string]string{"dsn": p.DSN} }

func (q *SQLQuery) urls() map[string]string { return map[string]string{"url": q.URL} }

func checkCredentials(entry Entry) error {
	carrier, ok := entry.(credentialURLs)
	if !ok {
		return nil
	}
	for field, raw := range carrier.urls() {
		if err := literalUserinfo(field, raw); err != nil {
			return err
		}
	}
	return nil
}

// checkProxies refuses two proxies on one fixed port, and two that export
// one variable, naming both.
func checkProxies(entries []Located, refusals *ValidationError) {
	ports, exports := map[int]Located{}, map[string]Located{}
	for _, at := range entries {
		proxy, ok := at.Entry.(proxyEntry)
		if !ok {
			continue
		}
		port, err := fixedPort(proxy.listen())
		refusals.add(at.Pointer()+"/listen", err)
		if first, taken := ports[port]; taken && port != 0 {
			refusals.add(at.Pointer()+"/listen", fmt.Errorf("port %s is also %s's", strconv.Itoa(port), first.Pointer()))
		}
		ports[port] = at
		for _, name := range proxy.exports() {
			if first, taken := exports[name]; taken {
				refusals.add(at.Pointer()+"/export", fmt.Errorf("%s is also exported by %s; set export so each proxy has its own", name, first.Pointer()))
			}
			exports[name] = at
		}
	}
}
