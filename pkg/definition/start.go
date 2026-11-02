package definition

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/flanksource/commons-db/connection/crd"
	"github.com/flanksource/commons-db/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/flanksource/telltale"
)

// Apply is options with def's captures armed: its specs, its process tree
// under what options already reads, its interval unless options sets one, and
// its inline connections as the run's own, with def recorded as the run's
// definition (its resolved spec). A connection:// reference must resolve in
// options.Connections, as the run reads it there.
func Apply(def *TraceDefinition, options telltale.StartOptions) (telltale.StartOptions, error) {
	if err := references(def, options); err != nil {
		return options, err
	}
	specs, err := Specs(def, Options{Connections: options.Connections})
	if err != nil {
		return options, err
	}
	resolved, err := Resolve(def, Options{Connections: options.Connections})
	if err != nil {
		return options, err
	}
	spec, err := json.Marshal(resolved.Definition.Spec)
	if err != nil {
		return options, fmt.Errorf("trace definition %s: encode resolved spec: %w", def.Name, err)
	}
	options.Traces = specs
	options.Definition = &telltale.DefinitionRef{Name: def.Name, ResourceVersion: def.ResourceVersion, Spec: spec}
	tree := def.Tree()
	options.Tree.Facets = options.Tree.Facets || tree.Facets
	if options.Tree.Environment == nil {
		options.Tree.Environment = tree.Environment
	}
	if options.Every == 0 && def.Spec.Every != "" {
		if options.Every, err = time.ParseDuration(def.Spec.Every); err != nil {
			return options, fmt.Errorf("trace definition %s: every: %w", def.Name, err)
		}
	}
	for _, ref := range def.Spec.Connections {
		if ref.Inline != nil {
			options.RunConnections = append(options.RunConnections, ref.Inline.resource())
		}
	}
	return options, nil
}

// references refuses a connection:// reference the run could not resolve.
func references(def *TraceDefinition, options telltale.StartOptions) error {
	var missing []string
	for _, ref := range def.Spec.Connections {
		if ref.Inline != nil {
			continue
		}
		if options.Connections == nil {
			missing = append(missing, ref.Reference)
			continue
		}
		found, err := options.Connections.Find(ref.Reference)
		if err != nil {
			return fmt.Errorf("trace definition %s: %w", def.Name, err)
		}
		if found == nil {
			missing = append(missing, ref.Reference)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("trace definition %s: the connection store defines no %s", def.Name, strings.Join(missing, ", "))
	}
	return nil
}

// resource is the Connection resource a run's store keeps for c: its secret
// references as references, resolved when a capture hydrates it.
func (c *InlineConnection) resource() crd.Connection {
	return crd.Connection{
		TypeMeta:   metav1.TypeMeta{APIVersion: crd.APIVersion, Kind: crd.Kind},
		ObjectMeta: metav1.ObjectMeta{Name: c.Name},
		Spec: crd.ConnectionSpec{
			Type: c.Type, URL: types.EnvVar{ValueStatic: c.URL}, Username: c.Username.EnvVar(), Password: c.Password.EnvVar(),
			Properties: c.Properties,
		},
	}
}
