package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Endpoint describes a local agent discovery file.
type Endpoint struct {
	PID      int               `json:"pid"`
	Addr     string            `json:"addr"`
	Token    string            `json:"token"`
	Service  string            `json:"service"`
	Runtime  Runtime           `json:"runtime"`
	Resource map[string]string `json:"resource"`
	Started  time.Time         `json:"started"`
}

var ErrStaleEndpoint = errors.New("stale agent endpoint")
var ErrNonLocalEndpoint = errors.New("non-local agent endpoint")

// LocalNetwork validates an agent address and returns its network.
func LocalNetwork(address string) (string, error) {
	if filepath.IsAbs(address) {
		return "unix", nil
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrNonLocalEndpoint, address)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return "", fmt.Errorf("%w: %s", ErrNonLocalEndpoint, address)
	}
	return "tcp", nil
}

// ReadEndpoint validates the discovery file against its authenticated local agent.
func ReadEndpoint(ctx context.Context, path string) (Endpoint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Endpoint{}, err
	}
	var endpoint Endpoint
	if err := json.Unmarshal(data, &endpoint); err != nil {
		return Endpoint{}, fmt.Errorf("decode endpoint: %w", err)
	}
	if endpoint.PID <= 0 || endpoint.Addr == "" || endpoint.Token == "" || endpoint.Started.IsZero() {
		return Endpoint{}, fmt.Errorf("%w: invalid file %s", ErrStaleEndpoint, path)
	}
	network, err := LocalNetwork(endpoint.Addr)
	if err != nil {
		return Endpoint{}, err
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Second)
		defer cancel()
	}
	client, err := Dial(ctx, network, endpoint.Addr, endpoint.Token)
	if err != nil {
		return Endpoint{}, fmt.Errorf("%w: %v", ErrStaleEndpoint, err)
	}
	defer client.Close()
	if client.Hello.AgentID != strconv.Itoa(endpoint.PID) || !client.Hello.Started.Equal(endpoint.Started) {
		return Endpoint{}, fmt.Errorf("%w: identity mismatch", ErrStaleEndpoint)
	}
	return endpoint, nil
}
