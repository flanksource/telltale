//go:build darwin || linux

package processcontrol

import "syscall"

func act(pid int, request Request) error {
	if request.Action == "terminate" {
		return syscall.Kill(pid, syscall.SIGTERM)
	}
	return syscall.Setpriority(syscall.PRIO_PROCESS, pid, *request.Nice)
}
