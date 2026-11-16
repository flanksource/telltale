package snapshot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	clickyexec "github.com/flanksource/clicky/exec"
	"github.com/flanksource/telltale/internal/safefs"
)

const supportEvents = "jdk.ExecutionSample,jdk.NativeMethodSample,jdk.ObjectAllocationSample,jdk.ObjectAllocationInNewTLAB,jdk.ObjectAllocationOutsideTLAB,jdk.JavaMonitorEnter,jdk.ThreadPark,jdk.GarbageCollection,jdk.GCHeapSummary"

// Scrubbing rebuilds constant pools as well as removing foreign events: another
// recording may enable sensitive events JVM-wide despite our own settings.
func scrub(ctx context.Context, data []byte) (result []byte, err error) {
	if err := os.MkdirAll(".tmp/jvm-snapshot", 0700); err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp(".tmp/jvm-snapshot", "profile-")
	if err != nil {
		return nil, err
	}
	defer func() {
		err = errors.Join(err, safefs.SafeRemove(directory, safefs.RemoveContext{AllowedRoot: ".tmp/jvm-snapshot"}))
	}()
	input, output := filepath.Join(directory, "raw.jfr"), filepath.Join(directory, "support.jfr")
	if err := os.WriteFile(input, data, 0600); err != nil {
		return nil, err
	}
	process := clickyexec.NewExec("jfr", "scrub", "--include-events", supportEvents, input, output).WithContext(ctx).WithCaptureLimit(4096).Run()
	if process.Err != nil {
		return nil, fmt.Errorf("scrub JFR (requires JDK 19+ jfr tool on Telltale host): %w: %s", process.Err, process.GetStderr())
	}
	info, err := os.Stat(output)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxArtifact {
		return nil, fmt.Errorf("scrubbed JFR exceeds 64 MiB")
	}
	return os.ReadFile(output)
}
