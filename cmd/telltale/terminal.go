package main

import (
	"fmt"
	"os"

	"golang.org/x/term"
)

func terminalRestore() (func(bool), error) {
	fd := int(os.Stdin.Fd())
	var original *term.State
	if term.IsTerminal(fd) {
		var err error
		original, err = term.GetState(fd)
		if err != nil {
			return nil, fmt.Errorf("save terminal state: %w", err)
		}
	}
	return func(alternate bool) {
		if original != nil {
			if err := term.Restore(fd, original); err != nil {
				_, _ = fmt.Fprintf(os.Stderr, "restore terminal: %v\n", err)
			}
		}
		if term.IsTerminal(int(os.Stderr.Fd())) {
			if alternate {
				_, _ = fmt.Fprint(os.Stderr, "\x1b[?1049l")
			}
			_, _ = fmt.Fprint(os.Stderr, "\x1b[?25h\x1b[0m")
		}
	}, nil
}
