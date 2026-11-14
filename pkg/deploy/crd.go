package deploy

import (
	"fmt"

	"github.com/flanksource/telltale/pkg/definition"
)

// CRDFile is where `task generate` writes the Deployer CRD, relative to the
// repository root.
const CRDFile = "crds/" + definition.Group + "_" + Plural + ".yaml"

// Schema is the Deployer's structural openAPIV3Schema. A javaContainer keeps
// the fields of its kind, which the kind's registered factory validates.
func Schema() (map[string]any, error) {
	schema, err := definition.SchemaOf(Deployer{})
	if err != nil {
		return nil, err
	}
	spec, _ := schema["properties"].(map[string]any)["spec"].(map[string]any)
	properties, _ := spec["properties"].(map[string]any)
	container, ok := properties["javaContainer"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("deployer schema has no spec.javaContainer")
	}
	container["x-kubernetes-preserve-unknown-fields"] = true
	container["required"] = []any{"kind"}
	return schema, nil
}

// CRD is the Deployer CustomResourceDefinition.
func CRD() ([]byte, error) {
	schema, err := Schema()
	if err != nil {
		return nil, err
	}
	return definition.CRDOf(Kind, Plural, "pkg/deploy", schema)
}
