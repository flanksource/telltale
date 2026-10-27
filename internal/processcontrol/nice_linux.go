package processcontrol

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"syscall"
)

func currentNice(pid int) (int, error) {
	priority, err := syscall.Getpriority(syscall.PRIO_PROCESS, pid)
	return 20 - priority, err
}
func renice(pid int, request Request) error {
	threads, err := os.ReadDir(fmt.Sprintf("/proc/%d/task", pid))
	if err != nil {
		return err
	}
	var errs []error
	for _, thread := range threads {
		tid, err := strconv.Atoi(thread.Name())
		if err != nil {
			return err
		}
		if request.LowerOnly {
			priority, err := currentNice(tid)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if priority >= *request.Nice {
				continue
			}
		}
		if err := syscall.Setpriority(syscall.PRIO_PROCESS, tid, *request.Nice); err != nil {
			errs = append(errs, fmt.Errorf("renice thread %d: %w", tid, err))
		}
	}
	return errors.Join(errs...)
}
