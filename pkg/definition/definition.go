// Package definition is the TraceDefinition resource: what telltale traces,
// kept apart from any one run of it. A definition names its captures by
// section (proxy, instrument, collect), platform group and capture type, each
// a typed list of its kind's own options; Specs compiles it to the TraceSpecs
// a run arms, and Store keeps definitions as YAML files in the config dirs.
package definition

import (
	"bytes"
	"encoding/json"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	"github.com/flanksource/telltale/pkg/traces"
)

// The resource's group, version and kind, as kubectl applies it.
const (
	Group      = "telltale.flanksource.com"
	Version    = "v1alpha1"
	APIVersion = Group + "/" + Version
	Kind       = "TraceDefinition"
	Plural     = "tracedefinitions"
)

// TraceDefinition is what a run traces: the connections its captures read,
// the proxies the command is routed through, what changes the target's
// runtime and what only reads it.
type TraceDefinition struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              Spec `json:"spec"`
	// adhoc marks the definition Inline makes of an ad-hoc start request,
	// which has no spec.connections for an entry to default to.
	adhoc bool
}

// Spec is a definition's captures. Nothing in it is about one run: the
// command, the pid and the duration are the execution's.
type Spec struct {
	Description string `json:"description,omitempty" clicky:"title=Description"`
	// Every is how often checkpointed captures are read; a run may override it.
	Every string `json:"every,omitempty" clicky:"title=Every,desc=How often checkpointed captures are read such as 30s"`
	// Connections are the defaults an entry that names no connection takes,
	// the one of its type.
	Connections []ConnectionRef `json:"connections,omitempty" clicky:"title=Connections,desc=Default connections: connection:// references or inline connections"`
	Proxy       Proxy           `json:"proxy,omitzero" clicky:"title=Proxy,desc=Proxies the command is routed through"`
	Instrument  Instrument      `json:"instrument,omitzero" clicky:"title=Instrument,desc=Captures that change the target's runtime"`
	Collect     Collect         `json:"collect,omitzero" clicky:"title=Collect,desc=Captures that only read"`
}

// Decode reads a definition from YAML or JSON, refusing a field it does not
// know, so a misspelt option is an error rather than a capture that is
// silently not configured.
func Decode(data []byte) (*TraceDefinition, error) {
	raw, err := yaml.YAMLToJSON(data)
	if err != nil {
		return nil, fmt.Errorf("decode trace definition: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var def TraceDefinition
	if err := decoder.Decode(&def); err != nil {
		return nil, fmt.Errorf("decode trace definition: %w", err)
	}
	if def.APIVersion != APIVersion || def.Kind != Kind {
		return nil, fmt.Errorf("decode trace definition: want apiVersion %s and kind %s, got %q and %q", APIVersion, Kind, def.APIVersion, def.Kind)
	}
	return &def, nil
}

// Encode is def as YAML, the form a store keeps.
func Encode(def *TraceDefinition) ([]byte, error) {
	data, err := yaml.Marshal(def)
	if err != nil {
		return nil, fmt.Errorf("encode trace definition %s: %w", def.Name, err)
	}
	return data, nil
}

// Tree is what the run-wide process tree reads of each process.
func (d *TraceDefinition) Tree() traces.TreeOptions {
	if d.Spec.Collect.Process.Tree == nil {
		return traces.TreeOptions{}
	}
	return *d.Spec.Collect.Process.Tree
}
