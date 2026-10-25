package groups

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"time"

	"github.com/creack/pty"
)

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
