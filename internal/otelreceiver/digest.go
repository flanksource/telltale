package otelreceiver

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
)

func nativeDigest(data []byte) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	canonicalize(value)
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(encoded)), nil
}

func canonicalize(value any) {
	switch value := value.(type) {
	case map[string]any:
		for _, item := range value {
			canonicalize(item)
		}
	case []any:
		keyed := len(value) > 0
		for _, item := range value {
			canonicalize(item)
			object, ok := item.(map[string]any)
			if !ok {
				keyed = false
				continue
			}
			if _, ok := object["key"].(string); !ok {
				keyed = false
			}
		}
		if keyed {
			sort.Slice(value, func(i, j int) bool {
				return value[i].(map[string]any)["key"].(string) < value[j].(map[string]any)["key"].(string)
			})
		}
	}
}
