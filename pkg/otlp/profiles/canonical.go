package profiles

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"

	"go.opentelemetry.io/collector/pdata/pprofile"
)

func canonicalNative(data []byte) (json.RawMessage, error) {
	profile, err := (&pprofile.ProtoUnmarshaler{}).UnmarshalProfiles(data)
	if err != nil {
		return nil, err
	}
	encoded, err := (&pprofile.JSONMarshaler{}).MarshalProfiles(profile)
	if err != nil {
		return nil, err
	}
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	dictionary, ok := value["dictionary"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("native profile dictionary is required")
	}
	delete(value, "dictionary")
	resolved, err := resolveNative(value, dictionary, 0)
	if err != nil {
		return nil, err
	}
	return json.Marshal(resolved)
}

var referenceTables = map[string]string{
	"keyStrindex": "stringTable", "stringValueStrindex": "stringTable", "typeStrindex": "stringTable", "unitStrindex": "stringTable",
	"nameStrindex": "stringTable", "systemNameStrindex": "stringTable", "filenameStrindex": "stringTable",
	"stackIndex": "stackTable", "linkIndex": "linkTable", "functionIndex": "functionTable", "mappingIndex": "mappingTable",
	"attributeIndices": "attributeTable", "locationIndices": "locationTable",
}

func resolveNative(value any, dictionary map[string]any, depth int) (any, error) {
	if depth > 64 {
		return nil, fmt.Errorf("native profile reference depth exceeded")
	}
	switch item := value.(type) {
	case map[string]any:
		resolved := make(map[string]any, len(item))
		for key, field := range item {
			var err error
			if table, reference := referenceTables[key]; reference {
				resolved[key], err = resolveReference(field, dictionary, table, depth+1)
			} else {
				resolved[key], err = resolveNative(field, dictionary, depth+1)
			}
			if err != nil {
				return nil, fmt.Errorf("native profile %s: %w", key, err)
			}
			if key == "attributeIndices" || key == "samples" || key == "attributes" {
				if err := sortNative(resolved[key]); err != nil {
					return nil, err
				}
			}
			if key == "kvlistValue" {
				if object, ok := resolved[key].(map[string]any); ok && object["values"] != nil {
					if err := sortNative(object["values"]); err != nil {
						return nil, err
					}
				}
			}
		}
		return resolved, nil
	case []any:
		resolved := make([]any, len(item))
		for index, field := range item {
			var err error
			resolved[index], err = resolveNative(field, dictionary, depth+1)
			if err != nil {
				return nil, err
			}
		}
		return resolved, nil
	default:
		return value, nil
	}
}

func resolveReference(value any, dictionary map[string]any, table string, depth int) (any, error) {
	if references, ok := value.([]any); ok {
		result := make([]any, len(references))
		for index, reference := range references {
			var err error
			result[index], err = resolveReference(reference, dictionary, table, depth+1)
			if err != nil {
				return nil, err
			}
		}
		return result, nil
	}
	number, ok := value.(json.Number)
	if !ok {
		return nil, fmt.Errorf("reference index must be an integer")
	}
	index, err := strconv.Atoi(string(number))
	if err != nil {
		return nil, err
	}
	entries, ok := dictionary[table].([]any)
	if !ok || index < 0 || index >= len(entries) {
		return nil, fmt.Errorf("%s index %d out of range", table, index)
	}
	return resolveNative(entries[index], dictionary, depth+1)
}

func sortNative(value any) error {
	entries, ok := value.([]any)
	if !ok {
		return fmt.Errorf("canonical native field must be a list")
	}
	encoded := make([]string, len(entries))
	indexed := make(map[string]any, len(entries))
	for index, entry := range entries {
		data, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		encoded[index] = string(data)
		indexed[string(data)] = entry
	}
	slices.Sort(encoded)
	for index, key := range encoded {
		entries[index] = indexed[key]
	}
	return nil
}
