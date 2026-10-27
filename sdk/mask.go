package telltale

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/flanksource/commons/logger"
)

const masked = "****"

var secretFlagText = regexp.MustCompile(`(?i)(--(?:password|token|secret|api-key)(?:=|\s+))("[^"]*"|'[^']*'|[^\s"']+)`)
var cookieText = regexp.MustCompile(`(?im)(\b(?:set-cookie|cookie)\s*:\s*)[^\r\n]+`)

// Mask removes recognizable secrets from text and JSON. JSON text is
// re-encoded only when a secret was masked in it, so text without secrets is
// returned exactly as it was.
func Mask(text string) string {
	var value any
	decoder := json.NewDecoder(bytes.NewBufferString(text))
	decoder.UseNumber()
	if json.Valid([]byte(text)) && decoder.Decode(&value) == nil {
		original, err := json.Marshal(value)
		if err == nil {
			if encoded, err := json.Marshal(maskJSON(value)); err == nil && !bytes.Equal(encoded, original) {
				return string(encoded)
			}
			return text
		}
	}
	text = secretFlagText.ReplaceAllString(text, "${1}"+masked)
	return logger.StripSecrets(cookieText.ReplaceAllString(text, "${1}"+masked))
}

func maskJSON(value any) any {
	switch v := value.(type) {
	case map[string]any:
		if name, ok := v["name"].(string); ok && (logger.IsSensitiveLogKey(name) || strings.EqualFold(name, "cookie") || strings.EqualFold(name, "set-cookie")) {
			if _, ok := v["value"]; ok {
				v["value"] = masked
			}
		}
		for key, nested := range v {
			if logger.IsSensitiveLogKey(key) || strings.EqualFold(key, "cookie") || strings.EqualFold(key, "set-cookie") {
				v[key] = masked
			} else {
				v[key] = maskJSON(nested)
			}
		}
	case []any:
		for i := range v {
			v[i] = maskJSON(v[i])
		}
	case string:
		return Mask(v)
	}
	return value
}

// MaskArgs masks command arguments. The first argument may be the executable
// name, allowing tool-specific flags such as mysql's -p to be recognized.
func MaskArgs(args []string) []string {
	if len(args) == 0 {
		return append([]string(nil), args...)
	}
	result := append([]string(nil), args...)
	tool := filepath.Base(args[0])
	passwordShort := tool == "mysql" || tool == "mariadb" || tool == "sshpass"
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if passwordShort && strings.HasPrefix(arg, "-p") && len(arg) > 2 && !strings.HasPrefix(arg, "--") {
			result[i] = "-p" + masked
			continue
		}
		if key, header, ok := strings.Cut(arg, "="); ok && key == "--header" {
			if name, _, ok := strings.Cut(header, ":"); ok && (strings.EqualFold(strings.TrimSpace(name), "authorization") || strings.EqualFold(strings.TrimSpace(name), "cookie")) {
				result[i] = key + "=" + name + ": " + masked
				continue
			}
		}
		if key, _, ok := strings.Cut(arg, "="); ok && (logger.IsSensitiveLogKey(key) || strings.EqualFold(key, "--api-key")) {
			result[i] = key + "=" + masked
			continue
		}
		if arg == "--password" || arg == "--token" || arg == "--secret" || arg == "--api-key" || (arg == "-p" && passwordShort) {
			if i+1 < len(args) {
				i++
				result[i] = masked
			}
			continue
		}
		if (arg == "-H" || arg == "--header") && i+1 < len(args) {
			i++
			if key, _, ok := strings.Cut(args[i], ":"); ok && (strings.EqualFold(strings.TrimSpace(key), "authorization") || strings.EqualFold(strings.TrimSpace(key), "cookie")) {
				result[i] = key + ": " + masked
			} else {
				result[i] = Mask(args[i])
			}
			continue
		}
		result[i] = Mask(arg)
	}
	return result
}
