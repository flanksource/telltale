//go:build !darwin && !linux

package rules

import "fmt"

func acquireLock() (func() error, error) {
	return nil, fmt.Errorf("rule enforcement requires macOS or Linux")
}
