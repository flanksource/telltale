//go:build !unix

package groups

import (
	"errors"
	"time"
)

func cpuTime() (time.Duration, error) {
	return 0, errors.New("read telltale's CPU time: getrusage is unix only")
}
