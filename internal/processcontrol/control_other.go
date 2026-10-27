//go:build !darwin && !linux

package processcontrol

import "errors"

func act(_ int, _ Request) error { return errors.New("process control requires macOS or Linux") }
