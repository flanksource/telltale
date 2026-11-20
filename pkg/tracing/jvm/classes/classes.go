// Package classes holds the typed results of the Java agent's class browsing,
// decompiling and hot-patching operations.
package classes

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/flanksource/clicky/api"
)

const (
	ClassesOp  = "jvm.classes"
	LoadersOp  = "jvm.loaders"
	SourceOp   = "jvm.classes.source"
	BytecodeOp = "jvm.classes.bytecode"
	CompileOp  = "jvm.classes.compile"
	PatchOp    = "jvm.classes.patch"
	RestoreOp  = "jvm.classes.restore"
	LeaseOp    = "jvm.classes.lease"
	PatchesOp  = "jvm.classes.patches"
)

// Results decode this package's operation results into their typed models.
var Results = map[string]func(json.RawMessage) (any, error){
	ClassesOp:  func(raw json.RawMessage) (any, error) { return DecodeClassPage(raw) },
	LoadersOp:  func(raw json.RawMessage) (any, error) { return DecodeLoaders(raw) },
	SourceOp:   func(raw json.RawMessage) (any, error) { return DecodeSource(raw) },
	BytecodeOp: func(raw json.RawMessage) (any, error) { return DecodeBytecode(raw) },
	CompileOp:  func(raw json.RawMessage) (any, error) { return DecodeCompile(raw) },
	PatchOp:    func(raw json.RawMessage) (any, error) { return DecodePatch(raw) },
	RestoreOp:  func(raw json.RawMessage) (any, error) { return DecodeRestore(raw) },
	LeaseOp:    func(raw json.RawMessage) (any, error) { return DecodeLease(raw) },
	PatchesOp:  func(raw json.RawMessage) (any, error) { return DecodePatches(raw) },
}

// decodeObject decodes an object result into out, refusing it unless every
// named field is present and not null.
func decodeObject(op string, raw json.RawMessage, out any, fields ...string) error {
	var present map[string]json.RawMessage
	if err := json.Unmarshal(raw, &present); err != nil || present == nil {
		return fmt.Errorf("%s returned %s, not its result object", op, raw)
	}
	for _, field := range fields {
		if value, ok := present[field]; !ok || bytes.Equal(value, []byte("null")) {
			return fmt.Errorf("%s result has no %q: %s", op, field, raw)
		}
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode %s result: %w", op, err)
	}
	return nil
}

// decodeRows decodes an array result, refusing null and any row valid rejects.
func decodeRows[T any](op string, raw json.RawMessage, valid func(T) bool) ([]T, error) {
	var rows []T
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("decode %s result: %w", op, err)
	}
	if rows == nil {
		return nil, fmt.Errorf("%s returned %s, not a list", op, raw)
	}
	for i, row := range rows {
		if !valid(row) {
			return nil, fmt.Errorf("%s row %d is incomplete: %s", op, i, raw)
		}
	}
	return rows, nil
}

// ClassPage is one page of the loaded classes matching a query. Total counts
// every match; PlatformExcluded is how many JDK/EE classes the listing hid.
type ClassPage struct {
	Total            int     `json:"total"`
	Offset           int     `json:"offset"`
	Limit            int     `json:"limit"`
	PlatformExcluded int     `json:"platformExcluded"`
	Classes          []Class `json:"classes"`
}

// Class is one loaded class and the hash of the loader that defined it.
type Class struct {
	Name   string `json:"name"`
	Loader string `json:"loader"`
}

func DecodeClassPage(raw json.RawMessage) (ClassPage, error) {
	var page ClassPage
	err := decodeObject(ClassesOp, raw, &page, "total", "offset", "limit", "platformExcluded", "classes")
	return page, err
}

func (Class) Columns() []api.ColumnDef {
	return []api.ColumnDef{
		api.Column("name").Label("Class").MaxWidth(100).Build(),
		api.Column("loader").Label("Loader").Build(),
	}
}

func (c Class) Row() map[string]any {
	return map[string]any{"name": c.Name, "loader": c.Loader}
}

func (p ClassPage) Pretty() api.Text {
	summary := fmt.Sprintf(" %d-%d of %d", p.Offset+1, p.Offset+len(p.Classes), p.Total)
	if len(p.Classes) == 0 {
		summary = fmt.Sprintf(" none at offset %d of %d", p.Offset, p.Total)
	}
	if p.PlatformExcluded > 0 {
		summary += fmt.Sprintf(" · %d platform classes hidden", p.PlatformExcluded)
	}
	text := api.Text{}.Append("Classes", "font-bold").Append(summary, "text-muted")
	if len(p.Classes) == 0 {
		return text
	}
	return text.NewLine().Add(api.NewTableFrom(p.Classes))
}

// Loaders are the JVM's class loaders, application code first. A virtual row
// stands for a whole family of LoaderCount loaders.
type Loaders []Loader

type Loader struct {
	Hash        string `json:"hash"`
	ClassName   string `json:"className"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Kind        string `json:"kind"`
	ClassCount  int    `json:"classCount"`
	Virtual     bool   `json:"virtual,omitempty"`
	LoaderCount int    `json:"loaderCount,omitempty"`
}

func DecodeLoaders(raw json.RawMessage) (Loaders, error) {
	return decodeRows(LoadersOp, raw, func(l Loader) bool { return l.Hash != "" && l.Kind != "" })
}

func (Loader) Columns() []api.ColumnDef {
	return []api.ColumnDef{
		api.Column("hash").Label("Loader").Build(),
		api.Column("label").Label("Label").Build(),
		api.Column("kind").Label("Kind").Build(),
		api.Column("classCount").Label("Classes").Build(),
		api.Column("className").Label("Class").MaxWidth(60).Build(),
		api.Column("description").Label("Packages").MaxWidth(60).Build(),
	}
}

func (l Loader) Row() map[string]any {
	return map[string]any{
		"hash":        l.Hash,
		"label":       l.Label,
		"kind":        l.Kind,
		"classCount":  l.ClassCount,
		"className":   l.ClassName,
		"description": l.Description,
	}
}

func (l Loaders) Pretty() api.Text {
	total := 0
	for _, loader := range l {
		total += loader.ClassCount
	}
	return api.Text{}.Append("Class loaders", "font-bold").
		Append(fmt.Sprintf(" %d loaders · %d classes", len(l), total), "text-muted").
		NewLine().Add(api.NewTableFrom(l))
}
