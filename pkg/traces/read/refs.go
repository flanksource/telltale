package read

import (
	"fmt"
	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/tracing"
)

// RunRefs enumerates a run's captures and named tables, including its process trace.
func RunRefs(info telltale.RunInfo, runtime *tracing.Runtime) ([]Ref, error) {
	refs := []Ref{}
	seen := map[string]bool{}
	primary := []tracing.TraceRef{}
	for _, capture := range info.Captures {
		primary = append(primary, capture.Ref)
	}
	if info.Process != nil {
		primary = append(primary, info.Process.Tree, info.Process.Stacks)
	}
	for _, ref := range primary {
		if _, exists := runtime.Kinds.Get(ref.Kind); !exists {
			return nil, fmt.Errorf("run %s uses unsupported trace kind %q; recordings with process_tree, process_samples or process_groups require the build that recorded them", info.RunID, ref.Kind)
		}
		for _, result := range runtime.Kinds.CaptureRefs(ref) {
			if err := result.Validate(); err != nil {
				return nil, err
			}
			if seen[result.Stream] {
				continue
			}
			seen[result.Stream] = true
			refs = append(refs, Ref{RunID: info.RunID, Runtime: runtime, TraceRef: result})
		}
	}
	return refs, nil
}
