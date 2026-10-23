package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	if err := clean(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func clean() error {
	if err := os.RemoveAll(".bin"); err != nil {
		return err
	}
	entries, err := os.ReadDir("webapp/dist")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == ".gitkeep" {
			continue
		}
		if err := os.RemoveAll(filepath.Join("webapp/dist", entry.Name())); err != nil {
			return err
		}
	}
	return nil
}
