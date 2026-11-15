package jvm

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/flanksource/clicky/rpc"
)

// catalog.json is the Java operation registry's built-in catalog, written by
// `./sdk/java/gradlew -p sdk/java jvmCatalog`; CatalogSnapshotTest fails when
// it is stale.
//
//go:embed catalog.json
var catalogJSON []byte

// Operation is one built-in operation of the Java diagnostics registry.
type Operation struct {
	Name string
	// Command is the CLI command, and API path, the registry serves the
	// operation as, such as [jvm patch apply] for jvm.classes.patch.
	Command     []string
	Title       string
	Description string
	Params      *rpc.OpenAPISchema
	Result      *rpc.OpenAPISchema
	Mutates     bool
}

var operations = mustDecodeCatalog(catalogJSON)

// Operations are the built-in operations every Java agent serves, in registry order.
func Operations() []Operation { return operations }

func mustDecodeCatalog(data []byte) []Operation {
	decoded, err := decodeCatalog(data)
	if err != nil {
		panic(fmt.Sprintf("embedded JVM operation catalog: %v", err))
	}
	return decoded
}

func decodeCatalog(data []byte) ([]Operation, error) {
	var entries []struct {
		Name         string         `json:"name"`
		Title        string         `json:"title"`
		Description  string         `json:"description"`
		Command      string         `json:"command"`
		ParamsSchema map[string]any `json:"paramsSchema"`
		ResultSchema map[string]any `json:"resultSchema"`
		Mutates      bool           `json:"mutates"`
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	decoded := make([]Operation, 0, len(entries))
	for _, entry := range entries {
		command := strings.Fields(entry.Command)
		if !strings.HasPrefix(entry.Name, "jvm.") || entry.Description == "" || len(command) < 2 || command[0] != "jvm" {
			return nil, fmt.Errorf("operation %q needs a jvm. name, a description and a jvm command", entry.Name)
		}
		params, err := schema(entry.ParamsSchema)
		if err != nil {
			return nil, fmt.Errorf("%s params: %w", entry.Name, err)
		}
		if params.Type != "object" {
			return nil, fmt.Errorf("%s params: expected an object schema, got %q", entry.Name, params.Type)
		}
		result, err := schema(entry.ResultSchema)
		if err != nil {
			return nil, fmt.Errorf("%s result: %w", entry.Name, err)
		}
		decoded = append(decoded, Operation{Name: entry.Name, Command: command, Title: entry.Title, Description: entry.Description, Params: params, Result: result, Mutates: entry.Mutates})
	}
	return decoded, nil
}

// schema decodes a JSON Schema into an OpenAPI schema. A closed object's
// `additionalProperties: false` has no OpenAPI field to land in and is
// dropped: only declared properties become flags or request fields anyway.
func schema(value map[string]any) (*rpc.OpenAPISchema, error) {
	if closed, ok := value["additionalProperties"].(bool); ok {
		if closed {
			return nil, fmt.Errorf("additionalProperties: true is not supported; use a schema")
		}
		delete(value, "additionalProperties")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var decoded rpc.OpenAPISchema
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}
	return &decoded, nil
}
