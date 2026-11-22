//go:build !darwin

package groups

import (
	"context"
	"io"
	"os/exec"
	"time"
)

func startNettop(context.Context, time.Duration) (io.ReadCloser, *exec.Cmd, error) {
	return nil, nil, ErrNetUnsupported
}

func sampleNettop(context.Context) (map[int]NetBytes, error) {
	return nil, ErrNetUnsupported
}
