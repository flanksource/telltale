package snapshot

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/flanksource/telltale/pkg/tracing"
)

var assignments = regexp.MustCompile(`(?i)([-D]*[a-z0-9_.-]*(?:password|passwd|secret|token|credential|api[_-]?key)[a-z0-9_.-]*\s*[=:]\s*)("[^"]*"|'[^']*'|[^\s,;]+)`)

func sanitized(raw []byte, text bool) ([]byte, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	value = tracing.MaskAttribute("snapshot", maskArguments(value))
	if text {
		content, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("expected text artifact")
		}
		return []byte(assignments.ReplaceAllString(content, "${1}[REDACTED]")), nil
	}
	// Inspect decoded strings so JSON escaping cannot hide argument assignments.
	return json.MarshalIndent(value, "", "  ")
}

func maskArguments(value any) any {
	switch v := value.(type) {
	case string:
		return assignments.ReplaceAllString(v, "${1}[REDACTED]")
	case map[string]any:
		if key, ok := v["key"].(string); ok {
			if child, exists := v["value"]; exists {
				v["value"] = tracing.MaskAttribute(key, child)
			}
		}
		for key, child := range v {
			v[key] = maskArguments(child)
		}
	case []any:
		for i, child := range v {
			v[i] = maskArguments(child)
		}
	}
	return value
}
