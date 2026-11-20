// Package agentops holds the typed results of the Java agent's probe,
// evaluation, extension and catalog operations.
package agentops

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Results decode this package's operation results into their typed models.
var Results = map[string]func(json.RawMessage) (any, error){
	InstrumentOp:        func(raw json.RawMessage) (any, error) { return DecodeInstrument(raw) },
	ProbesListOp:        func(raw json.RawMessage) (any, error) { return DecodeProbes(raw) },
	ProbesReadOp:        func(raw json.RawMessage) (any, error) { return DecodeProbeRead(raw) },
	ProbesFreezeOp:      func(raw json.RawMessage) (any, error) { return DecodeFreeze(raw) },
	GroovyEvalOp:        func(raw json.RawMessage) (any, error) { return DecodeGroovy(raw) },
	OgnlEvalOp:          func(raw json.RawMessage) (any, error) { return DecodeOgnl(raw) },
	ExtensionsListOp:    func(raw json.RawMessage) (any, error) { return DecodeExtensions(raw) },
	ExtensionsInstallOp: func(raw json.RawMessage) (any, error) { return DecodeExtensionInstall(raw, false) },
	ExtensionsReplaceOp: func(raw json.RawMessage) (any, error) { return DecodeExtensionInstall(raw, true) },
	ExtensionsUnloadOp:  func(raw json.RawMessage) (any, error) { return DecodeExtensionUnload(raw) },
	CatalogOp:           func(raw json.RawMessage) (any, error) { return DecodeCatalog(raw) },
}

// decode unmarshals op's result, refusing it unless every key in required is present.
func decode(op string, raw json.RawMessage, into any, required ...string) error {
	if err := json.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("decode %s result: %w", op, err)
	}
	if len(required) == 0 {
		return nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return fmt.Errorf("decode %s result: %w", op, err)
	}
	for _, key := range required {
		if _, ok := fields[key]; !ok {
			return fmt.Errorf("%s returned no %s: %s", op, key, raw)
		}
	}
	return nil
}

func shortType(name string) string { return name[strings.LastIndex(name, ".")+1:] }

// shortError names each exception class in text by its simple name.
func shortError(text string) string {
	parts := strings.Split(text, ": ")
	for i, part := range parts {
		simple := shortType(part)
		if strings.Contains(part, ".") && !strings.Contains(part, " ") && simple != "" && simple[0] >= 'A' && simple[0] <= 'Z' {
			parts[i] = simple
		}
	}
	return strings.Join(parts, ": ")
}

func clock(at time.Time) string { return at.Local().Format("15:04:05.000") }

func formatDuration(d time.Duration) string {
	switch {
	case d < time.Millisecond:
		return fmt.Sprintf("%dµs", (d+500*time.Nanosecond)/time.Microsecond)
	case d < time.Second:
		return fmt.Sprintf("%.1fms", float64(d)/float64(time.Millisecond))
	}
	return fmt.Sprintf("%.2fs", d.Seconds())
}

func plural(n int64, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
