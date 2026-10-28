package telltale

import "github.com/flanksource/telltale/pkg/traces"

func allCapturesFinished(captures []*traces.Capture) bool {
	for _, capture := range captures {
		if !capture.Finished() {
			return false
		}
	}
	return len(captures) > 0
}
