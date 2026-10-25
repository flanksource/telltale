package stacks

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	psprocess "github.com/shirou/gopsutil/v4/process"
)

// GopsPort is the port the gops agent of pid listens on, read from the port
// file the agent writes as github.com/google/gops does: $GOPS_CONFIG_DIR/<pid>,
// else <os.UserConfigDir>/gops/<pid>. A process without an agent is an error
// naming the file looked for. An agent that is killed leaves its file behind,
// so a file older than the process is an earlier process's with the same pid.
func GopsPort(pid int) (int, error) {
	dir := os.Getenv("GOPS_CONFIG_DIR")
	if dir == "" {
		config, err := os.UserConfigDir()
		if err != nil {
			return 0, fmt.Errorf("find gops agent of pid %d: %w", pid, err)
		}
		dir = filepath.Join(config, "gops")
	}
	path := filepath.Join(dir, strconv.Itoa(pid))
	file, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, fmt.Errorf("pid %d runs no gops agent: no %s", pid, path)
	}
	if err != nil {
		return 0, fmt.Errorf("find gops agent of pid %d: %w", pid, err)
	}
	started, err := startedAt(pid)
	if err != nil {
		return 0, fmt.Errorf("find gops agent of pid %d: %w", pid, err)
	}
	if file.ModTime().Before(started) {
		return 0, fmt.Errorf("pid %d runs no gops agent: %s was written at %s, before the process started at %s",
			pid, path, file.ModTime().UTC().Format(time.RFC3339), started.UTC().Format(time.RFC3339))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("find gops agent of pid %d: %w", pid, err)
	}
	port, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("gops port file %s: %q is not a port", path, strings.TrimSpace(string(data)))
	}
	return port, nil
}

// startedAt is when pid started, truncated to the second its port file's
// time is compared at.
func startedAt(pid int) (time.Time, error) {
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

// GopsAddress is the AddressFunc of local gops agents: the loopback address
// GopsPort names, which is where an agent listens by default.
func GopsAddress(pid int) (string, bool) {
	port, err := GopsPort(pid)
	if err != nil {
		return "", false
	}
	return "127.0.0.1:" + strconv.Itoa(port), true
}
