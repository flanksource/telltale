package snapshot

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/flanksource/telltale/pkg/tracing"
)

// secretWords name a secret in a key or an argument, in any case: tracing's
// masking misses camelCase property names such as trustStorePassword.
const secretWords = `(?:password|passwd|secret|token|credential|api[_-]?key)`

var (
	assignments = regexp.MustCompile(`(?i)([-D]*[a-z0-9_.-]*` + secretWords + `[a-z0-9_.-]*\s*[=:]\s*)("[^"]*"|'[^']*'|[^\s,;]+)`)
	secretKey   = regexp.MustCompile(`(?i)` + secretWords)
)

// masked is what tracing.MaskAttribute replaces a secret with.
const masked = "****"

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
				if _, scalar := child.(string); scalar && secretKey.MatchString(key) {
					v["value"] = masked
				} else {
					v["value"] = tracing.MaskAttribute(key, child)
				}
			}
		}
		for key, child := range v {
			if _, scalar := child.(string); scalar && secretKey.MatchString(key) {
				v[key] = masked
				continue
			}
			v[key] = maskArguments(child)
		}
	case []any:
		for i, child := range v {
			v[i] = maskArguments(child)
		}
	}
	return value
}
