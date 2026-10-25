//go:build unix

package groups

import (
	"fmt"
	"syscall"
	"time"
)

// cpuTime is the user and system CPU time of this process and of the
// children it has waited on.
func cpuTime() (time.Duration, error) {
	var total time.Duration
	for _, who := range []int{syscall.RUSAGE_SELF, syscall.RUSAGE_CHILDREN} {
		var usage syscall.Rusage
		if err := syscall.Getrusage(who, &usage); err != nil {
			return 0, fmt.Errorf("read telltale's CPU time: %w", err)
		}
		total += time.Duration(usage.Utime.Nano() + usage.Stime.Nano())
	}
	return total, nil
}
