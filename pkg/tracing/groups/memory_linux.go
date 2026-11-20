package groups

import (
	"context"
	"fmt"

	"github.com/shirou/gopsutil/v4/process"
)

func readMemory(ctx context.Context, classified []Group, readings map[string]Reading) error {
	for _, group := range classified {
		for _, member := range group.Members {
			if err := ctx.Err(); err != nil {
				return err
			}
			key := keyOf(member)
			reading := readings[key]
			p := process.Process{Pid: int32(member.PID)}
			memory, err := p.MemoryInfoWithContext(ctx)
			if err != nil {
				reading.MemoryError = fmt.Sprintf("PID %d swap: %v", member.PID, err)
			} else {
				reading.SwapBytes = &memory.Swap
			}
			readings[key] = reading
		}
	}
	return nil
}
