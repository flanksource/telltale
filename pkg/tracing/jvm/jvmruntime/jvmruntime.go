// Package jvmruntime holds the typed results of the Java agent's runtime
// operations: status, metrics, settings, resources, VM flags, and the thread
// sample, dump and stack.
package jvmruntime

import (
	"encoding/json"
	"fmt"
	"time"
)

// Results decode this package's operation results into their typed models.
var Results = map[string]func(json.RawMessage) (any, error){
	StatusOp:      func(raw json.RawMessage) (any, error) { return DecodeStatus(raw) },
	MetricsOp:     func(raw json.RawMessage) (any, error) { return DecodeMetrics(raw) },
	ThreadsOp:     func(raw json.RawMessage) (any, error) { return DecodeThreads(raw) },
	ThreadDumpOp:  func(raw json.RawMessage) (any, error) { return DecodeThreadDump(raw) },
	ThreadStackOp: func(raw json.RawMessage) (any, error) { return DecodeThreadStack(raw) },
	SettingsOp:    func(raw json.RawMessage) (any, error) { return DecodeSettings(raw) },
	ResourcesOp:   func(raw json.RawMessage) (any, error) { return DecodeResources(raw) },
	FlagsOp:       func(raw json.RawMessage) (any, error) { return DecodeFlags(raw) },
}

// decodeObject decodes an agent result object, refusing one that is missing
// (or nulls) any of the keys the agent always sends for op.
func decodeObject[T any](op string, raw json.RawMessage, keys ...string) (T, error) {
	var out T
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return out, fmt.Errorf("decode %s result: %w", op, err)
	}
	for _, key := range keys {
		if value, ok := fields[key]; !ok || string(value) == "null" {
			return out, fmt.Errorf("%s result has no %q: %s", op, key, raw)
		}
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, fmt.Errorf("decode %s result: %w", op, err)
	}
	return out, nil
}

// millis renders a millisecond count the way a reader scans it: 11ms, 1.4s, 4m20s.
func millis(ms int64) string {
	duration := time.Duration(ms) * time.Millisecond
	switch {
	case duration < time.Second:
		return fmt.Sprintf("%dms", ms)
	case duration < time.Minute:
		return duration.Round(100 * time.Millisecond).String()
	default:
		return duration.Round(time.Second).String()
	}
}
