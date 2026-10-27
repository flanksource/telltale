package agentclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"syscall"

	"github.com/flanksource/telltale/pkg/tracing/process/starttime"
	"github.com/flanksource/telltale/sdk/protocol"
)

type Endpoint = protocol.Endpoint

var errDeadProcess = errors.New("agent process is not running")

// Discover reads local agent discovery files from the user configuration directory.
func Discover() ([]Endpoint, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("agent config directory: %w", err)
	}
	return DiscoverIn(filepath.Join(dir, "telltale", "agents"))
}

// DiscoverIn reads local agent discovery files from dir.
func DiscoverIn(dir string) ([]Endpoint, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, fmt.Errorf("agent discovery: %w", err)
	}
	var endpoints []Endpoint
	for _, path := range paths {
		before, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("agent discovery %s: %w", path, err)
		}
		endpoint, err := readEndpoint(path)
		if err != nil {
			if errors.Is(err, protocol.ErrStaleEndpoint) {
				log.Printf("agent discovery: %v", err)
				if errors.Is(err, errDeadProcess) {
					if removeErr := removeDeadEndpoint(path, endpoint.PID, before); removeErr != nil {
						return nil, removeErr
					}
				}
				continue
			}
			return nil, err
		}
		endpoints = append(endpoints, endpoint)
	}
	return endpoints, nil
}

func removeDeadEndpoint(path string, pid int, before os.FileInfo) error {
	after, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect stale agent file %s: %w", path, err)
	}
	if !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) || before.Size() != after.Size() {
		return fmt.Errorf("stale agent file %s changed during discovery", path)
	}
	if !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
		return fmt.Errorf("stale agent pid %d changed during discovery", pid)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale agent file %s: %w", path, err)
	}
	return nil
}

func readEndpoint(path string) (Endpoint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Endpoint{}, fmt.Errorf("agent discovery %s: %w", path, err)
	}
	var endpoint Endpoint
	if err := json.Unmarshal(data, &endpoint); err != nil {
		return Endpoint{}, fmt.Errorf("agent discovery %s: %w", path, err)
	}
	if endpoint.PID <= 0 || endpoint.Addr == "" || endpoint.Token == "" || endpoint.Started.IsZero() {
		return Endpoint{}, fmt.Errorf("agent discovery %s: invalid endpoint", path)
	}
	if err := syscall.Kill(endpoint.PID, 0); errors.Is(err, syscall.ESRCH) {
		return endpoint, fmt.Errorf("%w: %w: pid %d", protocol.ErrStaleEndpoint, errDeadProcess, endpoint.PID)
	} else if err != nil && !errors.Is(err, syscall.EPERM) {
		return Endpoint{}, fmt.Errorf("agent discovery pid %d: %w", endpoint.PID, err)
	}
	started, err := starttime.Of(endpoint.PID)
	if err != nil {
		if errors.Is(syscall.Kill(endpoint.PID, 0), syscall.ESRCH) {
			return endpoint, fmt.Errorf("%w: %w: pid %d", protocol.ErrStaleEndpoint, errDeadProcess, endpoint.PID)
		}
		return Endpoint{}, fmt.Errorf("agent discovery pid %d start time: %w", endpoint.PID, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return Endpoint{}, fmt.Errorf("agent discovery %s: %w", path, err)
	}
	if info.ModTime().Before(started) {
		return Endpoint{}, fmt.Errorf("%w: %s predates pid %d", protocol.ErrStaleEndpoint, path, endpoint.PID)
	}
	return endpoint, nil
}
