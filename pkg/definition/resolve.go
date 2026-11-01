package definition

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/flanksource/commons-db/connection/store"

	"github.com/flanksource/telltale"
)

// ValidationError is a definition refused field by field, at the JSON
// pointers a form renders its errors at.
type ValidationError struct {
	Name   string                `json:"name,omitempty"`
	Fields []telltale.FieldError `json:"errors"`
}

func (e *ValidationError) Error() string {
	messages := make([]string, 0, len(e.Fields))
	for _, field := range e.Fields {
		messages = append(messages, field.InstancePath+": "+field.Message)
	}
	return fmt.Sprintf("invalid trace definition %q: %s", e.Name, strings.Join(messages, "; "))
}

func (e *ValidationError) add(pointer string, err error) {
	if err != nil {
		e.Fields = append(e.Fields, telltale.FieldError{InstancePath: pointer, Message: err.Error()})
	}
}

func (e *ValidationError) err() error {
	if len(e.Fields) == 0 {
		return nil
	}
	return e
}

// Options are what resolving a definition reads beyond the definition.
type Options struct {
	// Connections answers the type of each connection:// reference of
	// spec.connections, which a default connection is chosen by; nil knows
	// none, so an entry that would default to a reference is refused.
	Connections *store.Store
}

// Resolved is a definition with every entry named and every entry that names
// no connection given its default. Definition is a copy; the one resolved is
// never changed.
type Resolved struct {
	Definition *TraceDefinition
	Entries    []Located
}

// Resolve fills each entry's default name and default connection, refusing a
// name two entries resolve to and a default that is missing or ambiguous.
func Resolve(def *TraceDefinition, options Options) (*Resolved, error) {
	resolved, refusals := resolve(def, options)
	if err := refusals.err(); err != nil {
		return nil, err
	}
	return resolved, nil
}

func resolve(def *TraceDefinition, options Options) (*Resolved, *ValidationError) {
	refusals := &ValidationError{Name: def.Name}
	copied, err := deepCopy(def)
	if err != nil {
		refusals.add("", err)
		return nil, refusals
	}
	refusals.add("/metadata/name", validName(copied.Name))
	if copied.Spec.Every != "" {
		if every, err := time.ParseDuration(copied.Spec.Every); err != nil || every <= 0 {
			refusals.add("/spec/every", fmt.Errorf("%q is not a positive duration such as 30s", copied.Spec.Every))
		}
	}
	defaults := connectionDefaults(copied.Spec.Connections, options.Connections, refusals)
	entries := Walk(&copied.Spec)
	byName := map[string]Located{}
	for i := range entries {
		at := &entries[i]
		if at.Name == "" {
			at.Name = at.DefaultName(copied.Name)
			at.Entry.(interface{ setEntryName(string) }).setEntryName(at.Name)
		}
		if first, taken := byName[at.Name]; taken {
			refusals.add(at.Pointer()+"/name", fmt.Errorf("%q is also the name of %s", at.Name, first.Pointer()))
		}
		byName[at.Name] = *at
		if carrier, ok := at.Entry.(connectionEntry); ok {
			refusals.add(at.Pointer()+"/connection", defaults.fill(carrier.connection(*at)))
		}
	}
	return &Resolved{Definition: copied, Entries: entries}, refusals
}

// validName refuses a name that cannot be the definition's file name.
func validName(name string) error {
	if name == "" {
		return fmt.Errorf("a name is required")
	}
	if name != filepath.Base(name) || strings.HasPrefix(name, ".") || strings.ContainsAny(name, `\ `) {
		return fmt.Errorf("%q must be a plain file name that does not start with a dot", name)
	}
	return nil
}

func deepCopy(def *TraceDefinition) (*TraceDefinition, error) {
	data, err := json.Marshal(def)
	if err != nil {
		return nil, fmt.Errorf("copy trace definition: %w", err)
	}
	var copied TraceDefinition
	if err := json.Unmarshal(data, &copied); err != nil {
		return nil, fmt.Errorf("copy trace definition: %w", err)
	}
	return &copied, nil
}

// defaultConnection is one of spec.connections: its reference and its type,
// empty when no store could tell a reference's.
type defaultConnection struct {
	reference, connType string
}

type connectionSet []defaultConnection

// connectionDefaults are spec.connections with their types: an inline one's
// own, a reference's from the store, refusing an unknown or repeated one.
func connectionDefaults(refs []ConnectionRef, connections *store.Store, refusals *ValidationError) connectionSet {
	var set connectionSet
	for i, ref := range refs {
		pointer := "/spec/connections/" + strconv.Itoa(i)
		if err := ref.validate(); err != nil {
			refusals.add(pointer, err)
			continue
		}
		reference := referencePrefix + ref.Name()
		if slices.ContainsFunc(set, func(other defaultConnection) bool { return other.reference == reference }) {
			refusals.add(pointer, fmt.Errorf("%s is listed twice", reference))
			continue
		}
		known := defaultConnection{reference: reference}
		switch {
		case ref.Inline != nil:
			known.connType = ref.Inline.Type
		case connections != nil:
			stored, err := connections.Find(ref.Reference)
			switch {
			case err != nil:
				refusals.add(pointer, err)
			case stored == nil:
				refusals.add(pointer, fmt.Errorf("no connection %s in the connection store %v", ref.Reference, connections.Layers()))
			default:
				known.connType = stored.Connection.Type
			}
		}
		set = append(set, known)
	}
	return set
}

// fill gives an entry that names no connection the one default of its type,
// refusing none or several.
func (s connectionSet) fill(slot connectionSlot) error {
	if *slot.field != "" || slot.optional || slot.connType == "" {
		return nil
	}
	var matches, unknown []string
	for _, candidate := range s {
		switch candidate.connType {
		case slot.connType:
			matches = append(matches, candidate.reference)
		case "":
			unknown = append(unknown, candidate.reference)
		}
	}
	switch {
	case len(matches) == 1 && len(unknown) == 0:
		*slot.field = matches[0]
		return nil
	case len(matches) > 1:
		return fmt.Errorf("names no connection and %s are each a default %s connection; name one", strings.Join(matches, ", "), slot.connType)
	case len(unknown) > 0:
		return fmt.Errorf("names no connection, and without a connection store the type of %s is unknown; name one", strings.Join(unknown, ", "))
	}
	return fmt.Errorf("names no connection and spec.connections holds no %s connection", slot.connType)
}
