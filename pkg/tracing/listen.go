package tracing

import (
	"fmt"
	"net"
	"strconv"
)

// ValidateListen is the static check of the host:port a proxy capture binds:
// empty takes a free loopback port, and a port of 0 a free one on the host.
// It binds nothing, so a port another process holds fails only the start.
func ValidateListen(address string) error {
	if address == "" {
		return nil
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("listen %q: want host:port such as 127.0.0.1:18080: %w", address, err)
	}
	if host == "" {
		return fmt.Errorf("listen %q names no host: give one such as 127.0.0.1, so the proxy is not open to the network by accident", address)
	}
	if number, err := strconv.Atoi(port); err != nil || number < 0 || number > 65535 {
		return fmt.Errorf("listen %q: port %q is not between 0 and 65535", address, port)
	}
	return nil
}

// Listen binds address for a proxy capture named what, naming the address
// when it is taken.
func Listen(what, address string) (net.Listener, error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("%s listen %s: %w", what, address, err)
	}
	return listener, nil
}
