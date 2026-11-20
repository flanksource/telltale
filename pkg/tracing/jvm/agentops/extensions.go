package agentops

import (
	"encoding/json"
	"fmt"

	"github.com/flanksource/clicky/api"
)

const (
	// ExtensionsListOp lists the diagnostic extensions loaded into the agent.
	ExtensionsListOp = "jvm.extensions.list"
	// ExtensionsInstallOp loads a new diagnostic extension JAR.
	ExtensionsInstallOp = "jvm.extensions.install"
	// ExtensionsReplaceOp swaps a loaded extension for a new JAR of the same id.
	ExtensionsReplaceOp = "jvm.extensions.replace"
	// ExtensionsUnloadOp unloads a diagnostic extension.
	ExtensionsUnloadOp = "jvm.extensions.unload"
)

// Extensions are the loaded diagnostic extensions.
type Extensions []Extension

// Extension is one loaded JAR: Digest is its SHA-256, Active its in-flight
// calls and scripts, Status "active" or "failed" after a cleanup failure.
type Extension struct {
	ID         string   `json:"id"`
	Version    string   `json:"version"`
	Digest     string   `json:"digest"`
	Active     int      `json:"active"`
	Status     string   `json:"status"`
	Operations []string `json:"operations"`
}

// DecodeExtensions decodes the agent's extension list.
func DecodeExtensions(raw json.RawMessage) (Extensions, error) {
	var result Extensions
	if err := decode(ExtensionsListOp, raw, &result); err != nil {
		return result, err
	}
	if result == nil {
		return result, fmt.Errorf("%s returned no extension list: %s", ExtensionsListOp, raw)
	}
	for _, extension := range result {
		if extension.ID == "" {
			return result, fmt.Errorf("%s returned an extension without an id: %s", ExtensionsListOp, raw)
		}
	}
	return result, nil
}

func (Extension) Columns() []api.ColumnDef {
	return []api.ColumnDef{
		api.Column("id").Label("ID").Build(),
		api.Column("version").Label("Version").Build(),
		api.Column("status").Label("Status").Build(),
		api.Column("active").Label("Active").Build(),
		api.Column("operations").Label("Operations").Build(),
		api.Column("digest").Label("Digest").Build(),
	}
}

func (e Extension) Row() map[string]any {
	status := api.Text{}.Append(e.Status, "text-green-600")
	if e.Status != "active" {
		status = api.Text{}.Append(e.Status, "text-red-600")
	}
	digest := e.Digest
	if len(digest) > 12 {
		digest = digest[:12]
	}
	return map[string]any{
		"id":         e.ID,
		"version":    e.Version,
		"status":     status,
		"active":     e.Active,
		"operations": len(e.Operations),
		"digest":     digest,
	}
}

// Pretty is the extensions under a heading, or one muted line when none is loaded.
func (e Extensions) Pretty() api.Text {
	if len(e) == 0 {
		return api.Text{}.Append("no extensions loaded", "text-muted")
	}
	return api.Text{}.Append("Extensions", "font-bold").Append(fmt.Sprintf(" %d loaded", len(e)), "text-muted").
		NewLine().Add(api.NewTableFrom(e))
}

// ExtensionInstalled is the id an install or replace loaded.
type ExtensionInstalled struct {
	Installed string `json:"installed"`
	replaced  bool
}

// DecodeExtensionInstall decodes an install's result, or a replace's when replaced.
func DecodeExtensionInstall(raw json.RawMessage, replaced bool) (ExtensionInstalled, error) {
	op := ExtensionsInstallOp
	if replaced {
		op = ExtensionsReplaceOp
	}
	result := ExtensionInstalled{replaced: replaced}
	if err := decode(op, raw, &result); err != nil {
		return result, err
	}
	if result.Installed == "" {
		return result, fmt.Errorf("%s returned no installed extension: %s", op, raw)
	}
	return result, nil
}

// Pretty is the install acknowledged on one line.
func (e ExtensionInstalled) Pretty() api.Text {
	verb := "installed"
	if e.replaced {
		verb = "replaced"
	}
	return api.Text{}.Append("✓ "+verb+" extension "+e.Installed, "text-green-600")
}

// ExtensionUnloaded is the id an unload removed.
type ExtensionUnloaded struct {
	Unloaded string `json:"unloaded"`
}

// DecodeExtensionUnload decodes an unload's result.
func DecodeExtensionUnload(raw json.RawMessage) (ExtensionUnloaded, error) {
	var result ExtensionUnloaded
	if err := decode(ExtensionsUnloadOp, raw, &result); err != nil {
		return result, err
	}
	if result.Unloaded == "" {
		return result, fmt.Errorf("%s returned no unloaded extension: %s", ExtensionsUnloadOp, raw)
	}
	return result, nil
}

// Pretty is the unload acknowledged on one line.
func (e ExtensionUnloaded) Pretty() api.Text {
	return api.Text{}.Append("✓ unloaded extension "+e.Unloaded, "text-green-600")
}
