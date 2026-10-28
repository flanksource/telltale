package telltale

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/flanksource/telltale/sdk/protocol"
)

func removeStaleSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("agent socket %s is not a socket", path)
	}
	stream, err := net.DialTimeout("unix", path, 250*time.Millisecond)
	if err == nil {
		_ = stream.Close()
		return fmt.Errorf("agent socket %s is active", path)
	}
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, os.ErrNotExist) {
		return os.Remove(path)
	}
	return fmt.Errorf("inspect agent socket %s: %w", path, err)
}

func removeStaleFile(path string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var endpoint protocol.Endpoint
	if err := json.Unmarshal(data, &endpoint); err != nil {
		return fmt.Errorf("inspect discovery file: %w", err)
	}
	if endpoint.PID != os.Getpid() {
		return fmt.Errorf("discovery file has unexpected pid %d", endpoint.PID)
	}
	network := "unix"
	if !filepath.IsAbs(endpoint.Addr) {
		network = "tcp"
		host, _, err := net.SplitHostPort(endpoint.Addr)
		if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
			return fmt.Errorf("discovery file has non-local address")
		}
	}
	stream, err := net.DialTimeout(network, endpoint.Addr, 250*time.Millisecond)
	if err == nil {
		_ = stream.Close()
		return fmt.Errorf("agent discovery file %s is active", path)
	}
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, os.ErrNotExist) {
		return os.Remove(path)
	}
	return fmt.Errorf("inspect agent discovery file %s: %w", path, err)
}
