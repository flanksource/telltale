package processcontrol

import "syscall"

func currentNice(pid int) (int, error) { return syscall.Getpriority(syscall.PRIO_PROCESS, pid) }
func renice(pid int, request Request) error {
	if request.LowerOnly {
		priority, err := currentNice(pid)
		if err != nil {
			return err
		}
		if priority >= *request.Nice {
			return nil
		}
	}
	return syscall.Setpriority(syscall.PRIO_PROCESS, pid, *request.Nice)
}
