package agentops

import (
	"encoding/json"
	"fmt"

	"github.com/flanksource/clicky/api"
)

// CatalogOp describes every operation the agent and its extensions serve.
const CatalogOp = "jvm.catalog"

// Catalog is the agent's operations, built-in first, then each extension's.
type Catalog []Operation

// Operation is one catalog descriptor. Extension names the extension that
// serves it; it is empty for a built-in operation.
type Operation struct {
	Name         string          `json:"name"`
	Title        string          `json:"title"`
	Description  string          `json:"description"`
	Command      string          `json:"command,omitempty"`
	ParamsSchema json.RawMessage `json:"paramsSchema"`
	ResultSchema json.RawMessage `json:"resultSchema"`
	Mutates      bool            `json:"mutates"`
	Scope        string          `json:"scope"`
	Extension    string          `json:"extension,omitempty"`
}

// DecodeCatalog decodes the agent's catalog, refusing an operation without a name.
func DecodeCatalog(raw json.RawMessage) (Catalog, error) {
	var result Catalog
	if err := decode(CatalogOp, raw, &result); err != nil {
		return result, err
	}
	if result == nil {
		return result, fmt.Errorf("%s returned no operations: %s", CatalogOp, raw)
	}
	for i, operation := range result {
		if operation.Name == "" {
			return result, fmt.Errorf("%s returned operation %d without a name: %s", CatalogOp, i, raw)
		}
	}
	return result, nil
}

func (Operation) Columns() []api.ColumnDef {
	return []api.ColumnDef{
		api.Column("name").Label("Operation").Build(),
		api.Column("command").Label("Command").Build(),
		api.Column("scope").Label("Scope").Build(),
		api.Column("mutates").Label("").Build(),
		api.Column("description").Label("Description").MaxWidth(80).Build(),
	}
}

func (o Operation) Row() map[string]any {
	mutates := api.Text{}
	if o.Mutates {
		mutates = mutates.Append("mutates", "text-yellow-600")
	}
	return map[string]any{
		"name":        o.Name,
		"command":     o.Command,
		"scope":       o.Scope,
		"mutates":     mutates,
		"description": o.Description,
	}
}

// Pretty is the operations under a heading counting those that mutate.
func (c Catalog) Pretty() api.Text {
	mutating := 0
	for _, operation := range c {
		if operation.Mutates {
			mutating++
		}
	}
	return api.Text{}.Append("Catalog", "font-bold").Append(fmt.Sprintf(" %d operations · %d mutates", len(c), mutating), "text-muted").
		NewLine().Add(api.NewTableFrom(c))
}
