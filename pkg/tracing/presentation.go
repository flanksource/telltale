package tracing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/flanksource/clicky"
	"github.com/flanksource/clicky/api"
	"github.com/flanksource/commons-db/query"
)

type RecordPresentation struct {
	Summary api.Text
	Detail  api.Textable
}

// Present restores typed presentation from the processed, stored record. Schema
// fields provide the standard presentation for kinds without Pretty methods.
func (k *Kinds) Present(kind string, row query.Row) (RecordPresentation, error) {
	plugin, ok := k.Get(kind)
	if !ok {
		return RecordPresentation{}, fmt.Errorf("%w: %s", ErrUnknownKind, kind)
	}
	encoded, err := json.Marshal(row)
	if err != nil {
		return RecordPresentation{}, err
	}
	var stored query.Row
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(&stored); err != nil {
		return RecordPresentation{}, err
	}
	jsonAsText(stored, plugin.jsonFields())
	encoded, err = json.Marshal(stored)
	if err != nil {
		return RecordPresentation{}, err
	}
	if presenter, ok := plugin.(interface {
		presentRecord([]byte) (RecordPresentation, bool, error)
	}); ok {
		if presentation, handled, err := presenter.presentRecord(encoded); handled || err != nil {
			return presentation, err
		}
	}
	value := reflect.New(plugin.recordType())
	if err := json.Unmarshal(encoded, value.Interface()); err != nil {
		return RecordPresentation{}, fmt.Errorf("present %s: %w", kind, err)
	}
	if value.Elem().Kind() == reflect.Pointer {
		value = value.Elem()
	}
	var summary api.Text
	if pretty, ok := value.Interface().(api.Pretty); ok {
		summary = pretty.Pretty()
	} else if table, ok := value.Interface().(api.TableProvider); ok {
		summary = prettyRecordFields(table.Columns(), table.Row())
	} else {
		schema, err := plugin.Schema(kind)
		if err != nil {
			return RecordPresentation{}, err
		}
		columns := []api.ColumnDef{}
		for _, column := range schema.Columns {
			if column.Hidden || column.Type == query.ColumnTypeJSON || row[column.Name] == nil {
				continue
			}
			columns = append(columns, api.Column(column.Name).Label(column.Label).Build())
		}
		summary = prettyRecordFields(columns, row)
	}
	native, err := json.MarshalIndent(row, "", "  ")
	if err != nil {
		return RecordPresentation{}, err
	}
	var detail api.Textable = clicky.CodeBlock("json", string(native))
	if full, ok := value.Interface().(api.PrettyFull); ok {
		detail = full.PrettyFull()
	}
	return RecordPresentation{Summary: summary, Detail: detail}, nil
}

func prettyRecordFields(columns []api.ColumnDef, row map[string]any) api.Text {
	text := api.Text{}
	count := 0
	for _, column := range columns {
		if column.Hidden || column.DefaultHidden || row[column.Name] == nil {
			continue
		}
		if count > 0 {
			text = text.Append(" · ", "muted")
		}
		value, ok := row[column.Name].(api.Textable)
		if !ok {
			value = clicky.Human(row[column.Name])
		}
		text = text.Append(column.DisplayLabel()+"=", "muted").Add(value)
		count++
		if count == 4 {
			break
		}
	}
	return text
}
