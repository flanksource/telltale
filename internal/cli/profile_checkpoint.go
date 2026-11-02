package cli

import (
	"encoding/json"
	"fmt"

	"github.com/flanksource/clicky/rpc"
	"github.com/flanksource/telltale/pkg/tracing/goprof"
	"github.com/spf13/pflag"
)

type profileCheckpointFlags struct {
	defaults goprof.CheckpointParams
	bound    paramFlags
}

func bindProfileCheckpoint(flags *pflag.FlagSet, defaults goprof.CheckpointParams) *profileCheckpointFlags {
	schema := defaults.Schema()
	selected := &rpc.OpenAPISchema{Type: "object", Properties: map[string]*rpc.OpenAPISchema{}}
	for _, name := range []string{"profile", "seconds", "source"} {
		selected.Properties[name] = schema.Properties[name]
	}
	bound, err := bindParamFlags(flags, selected)
	if err != nil {
		panic(fmt.Sprintf("trace kind goprof checkpoint params: %v", err))
	}
	return &profileCheckpointFlags{defaults: defaults, bound: bound}
}

func (p *profileCheckpointFlags) params() (json.RawMessage, error) {
	params := p.defaults
	raw, err := p.bound.params()
	if err != nil {
		return nil, err
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, fmt.Errorf("checkpoint params: %w", err)
		}
	}
	if params.Seconds < 0 || (params.Seconds != 0 && params.Profile != "cpu") {
		return nil, fmt.Errorf("--seconds requires a CPU profile and must be positive")
	}
	return json.Marshal(params)
}
