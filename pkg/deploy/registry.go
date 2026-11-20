package deploy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"

	dbcontext "github.com/flanksource/commons-db/context"
)

// JavaContainer gets the agent into one target's JVM.
type JavaContainer interface {
	// Deploy installs the agent into target, or confirms it is already
	// there with this provision, and reports where it executes.
	Deploy(ctx dbcontext.Context, target Target, provision Provision) TargetStatus
	// Client is the HTTP client executions reach a deployed target with.
	Client(ctx dbcontext.Context, namespace string) (*http.Client, error)
}

// Factory decodes one kind's javaContainer spec.
type Factory func(spec json.RawMessage) (JavaContainer, error)

var (
	containersMu sync.RWMutex
	containers   = map[string]Factory{}
)

// RegisterJavaContainer adds a javaContainer kind; a kind registers once.
func RegisterJavaContainer(kind string, factory Factory) {
	containersMu.Lock()
	defer containersMu.Unlock()
	if kind == "" || factory == nil {
		panic("deploy: RegisterJavaContainer needs a kind and a factory")
	}
	if _, exists := containers[kind]; exists {
		panic(fmt.Sprintf("deploy: javaContainer kind %q registered twice", kind))
	}
	containers[kind] = factory
}

// JavaContainerSpec is a javaContainer object: its kind picks the factory
// that decodes the rest of it.
type JavaContainerSpec struct {
	Kind string `json:"kind" clicky:"title=Kind,desc=tomcat attach or javaagent"`
	raw  json.RawMessage
}

// NewJavaContainerSpec is the spec of kind with the given fields.
func NewJavaContainerSpec(spec any) (JavaContainerSpec, error) {
	data, err := json.Marshal(spec)
	if err != nil {
		return JavaContainerSpec{}, err
	}
	var decoded JavaContainerSpec
	return decoded, json.Unmarshal(data, &decoded)
}

func (s *JavaContainerSpec) UnmarshalJSON(data []byte) error {
	var head struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return err
	}
	s.Kind, s.raw = head.Kind, bytes.Clone(data)
	return nil
}

func (s JavaContainerSpec) MarshalJSON() ([]byte, error) {
	if s.raw == nil {
		return json.Marshal(map[string]string{"kind": s.Kind})
	}
	return s.raw, nil
}

// Container builds the registered java container for the spec.
func (s JavaContainerSpec) Container() (JavaContainer, error) {
	containersMu.RLock()
	factory, ok := containers[s.Kind]
	kinds := slices.Sorted(maps.Keys(containers))
	containersMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("javaContainer kind %q is not registered (registered: %s)", s.Kind, strings.Join(kinds, ", "))
	}
	container, err := factory(s.raw)
	if err != nil {
		return nil, fmt.Errorf("javaContainer %s: %w", s.Kind, err)
	}
	return container, nil
}

// decodeStrict decodes a kind's spec, refusing a field the kind does not know.
func decodeStrict(data json.RawMessage, into any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode(into)
}
