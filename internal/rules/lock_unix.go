//go:build darwin || linux

package rules

import (
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
)

func acquireLock() (func() error, error) {
	root, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	root = filepath.Join(root, "telltale")
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(root, fmt.Sprintf("enforcement-%d.lock", os.Geteuid())), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return nil, fmt.Errorf("another rules enforcer holds the user lock: %w", errors.Join(err, file.Close()))
	}
	return file.Close, nil
}
