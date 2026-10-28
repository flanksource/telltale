package cli

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/flanksource/telltale/pkg/tracing/goprof"
	"github.com/spf13/pflag"
)

type profileCheckpointFlags struct{ paramsValue goprof.CheckpointParams }

func bindProfileCheckpoint(flags *pflag.FlagSet, defaults goprof.CheckpointParams) *profileCheckpointFlags {
	bound := &profileCheckpointFlags{paramsValue: defaults}
	flags.StringVar(&bound.paramsValue.Profile, "profile", defaults.Profile, "Go profile read at each checkpoint")
	flags.IntVar(&bound.paramsValue.Seconds, "seconds", defaults.Seconds, "Duration of a CPU profile window in seconds")
	flags.StringVar(&bound.paramsValue.Source, "source", defaults.Source, "Process profile source")
	return bound
}

func (p *profileCheckpointFlags) params() (json.RawMessage, error) {
	if !slices.Contains(goprof.Profiles, p.paramsValue.Profile) {
		return nil, fmt.Errorf("--profile: unsupported profile %q", p.paramsValue.Profile)
	}
	if p.paramsValue.Seconds < 0 || (p.paramsValue.Seconds != 0 && p.paramsValue.Profile != "cpu") {
		return nil, fmt.Errorf("--seconds requires a CPU profile and must be positive")
	}
	return json.Marshal(p.paramsValue)
}
