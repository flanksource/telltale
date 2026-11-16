package jvm

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/flanksource/clicky/api"
	"github.com/flanksource/telltale/pkg/tracing/jvm/agentops"
	"github.com/flanksource/telltale/pkg/tracing/jvm/classes"
	"github.com/flanksource/telltale/pkg/tracing/jvm/jmx"
	"github.com/flanksource/telltale/pkg/tracing/jvm/jvmruntime"
)

// results decode the operations that have a typed model, so they print as
// records in any output format instead of raw JSON.
var results = mergeResults(
	map[string]func(json.RawMessage) (any, error){
		HeapHistogramOp: func(raw json.RawMessage) (any, error) { return DecodeHeapHistogram(raw) },
	},
	jvmruntime.Results, classes.Results, jmx.Results, agentops.Results,
)

func mergeResults(registries ...map[string]func(json.RawMessage) (any, error)) map[string]func(json.RawMessage) (any, error) {
	merged := map[string]func(json.RawMessage) (any, error){}
	for _, registry := range registries {
		for operation, decode := range registry {
			if _, taken := merged[operation]; taken {
				panic(fmt.Sprintf("operation %s has two result models", operation))
			}
			merged[operation] = decode
		}
	}
	return merged
}

// requested is a result that names what its request changed, for an agent
// that answers only that it applied it.
type requested interface {
	Requested(params json.RawMessage) (any, error)
}

// Result decodes an operation's result into its typed model, or keeps one
// without a model, such as an extension's, as the RawResult the agent sent.
// params are the request's, for a result that names what it changed.
func Result(operation string, params, raw json.RawMessage) (any, error) {
	if decode, ok := results[operation]; ok {
		result, err := decode(raw)
		if err != nil {
			return nil, err
		}
		if named, ok := result.(requested); ok {
			return named.Requested(params)
		}
		return result, nil
	}
	if len(raw) > 0 && !json.Valid(raw) {
		return nil, fmt.Errorf("%s returned a result that is not JSON: %.200s", operation, raw)
	}
	return RawResult(raw), nil
}

// RawResult is a result without a typed model: --format json prints it as
// sent, and the terminal indented and highlighted.
type RawResult json.RawMessage

func (r RawResult) MarshalJSON() ([]byte, error) {
	if len(r) == 0 {
		return []byte("null"), nil
	}
	return r, nil
}

func (r RawResult) Pretty() api.Text {
	if len(r) == 0 || string(r) == "null" {
		return api.Text{}.Append("✓ done", "text-green-600").Append(" · no result", "text-muted")
	}
	var indented bytes.Buffer
	if err := json.Indent(&indented, r, "", "  "); err != nil {
		return api.Text{}.Append(string(r))
	}
	return api.Text{}.Add(api.CodeBlock("json", indented.String()))
}
