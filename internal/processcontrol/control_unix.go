//go:build darwin || linux

package processcontrol

import (
	"syscall"
)

func validSignal(name string) bool { _, ok := signals[name]; return ok }

var signals = map[string]syscall.Signal{"SIGHUP": syscall.SIGHUP, "SIGINT": syscall.SIGINT, "SIGTERM": syscall.SIGTERM, "SIGKILL": syscall.SIGKILL}

func act(pid int, request Request) error {
	if request.Action == "terminate" {
		return syscall.Kill(pid, syscall.SIGTERM)
	}
	if request.Action == "signal" {
		return syscall.Kill(pid, signals[request.Signal])
	}
	return renice(pid, request)
}
