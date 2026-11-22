package groups

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"time"

	"github.com/creack/pty"
)

func sampleNettop(ctx context.Context) (map[int]NetBytes, error) {
	cmd := exec.CommandContext(ctx, "nettop", "-P", "-n", "-L", "1", "-x", "-J", "bytes_in,bytes_out")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("sample nettop: %w", err)
	}
	var counters map[int]NetBytes
	if err := ParseNettop(bytes.NewReader(output), func(block map[int]NetBytes) { counters = block }); err != nil {
		return nil, err
	}
	if counters == nil {
		return nil, fmt.Errorf("nettop returned no network sample")
	}
	return counters, nil
}

// startNettop logs each process's network byte deltas every interval until
// ctx is done. nettop holds back its output on a pipe until it exits, so it
// writes to a terminal, which it flushes each block to.
func startNettop(ctx context.Context, interval time.Duration) (io.ReadCloser, *exec.Cmd, error) {
	seconds := strconv.Itoa(int(interval.Round(time.Second) / time.Second))
	cmd := exec.CommandContext(ctx, "nettop", "-P", "-d", "-n", "-L", "0", "-s", seconds, "-x", "-J", "bytes_in,bytes_out")
	terminal, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 50, Cols: 512})
	if err != nil {
		return nil, nil, fmt.Errorf("start nettop: %w", err)
	}
	return terminal, cmd, nil
}
