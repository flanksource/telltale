package groups

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

func readMemory(ctx context.Context, classified []Group, readings map[string]Reading) error {
	var stdout, stderr bytes.Buffer
	// macOS accounts compressed and swapped-out pages together in CMPRS;
	// it does not expose a separate per-process disk-swap total.
	cmd := exec.CommandContext(ctx, "/usr/bin/top", "-l", "1", "-stats", "pid,cmprs", "-R", "-F")
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("read compressed memory: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	compressed, err := parseCompressed(&stdout)
	if err != nil {
		return err
	}
	for _, group := range classified {
		for _, member := range group.Members {
			key := keyOf(member)
			reading := readings[key]
			reading.CompressedBytes = compressed[member.PID]
			if reading.CompressedBytes == nil {
				reading.MemoryError = fmt.Sprintf("PID %d absent from compressed-memory listing", member.PID)
			}
			readings[key] = reading
		}
	}
	return nil
}
