// Package starttime reads process creation times for local discovery.
package starttime

import (
	"fmt"
	"time"

	psprocess "github.com/shirou/gopsutil/v4/process"
)

// Of returns the process start time truncated to the second.
func Of(pid int) (time.Time, error) {
	handle, err := psprocess.NewProcess(int32(pid))
	if err != nil {
		return time.Time{}, err
	}
	created, err := handle.CreateTime()
	if err != nil {
		return time.Time{}, fmt.Errorf("start time: %w", err)
	}
	return time.UnixMilli(created).Truncate(time.Second), nil
}
