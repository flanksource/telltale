package safefs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type RemoveContext struct {
	AllowedRoot string
	Protected   []string
}

func SafeRemove(path string, guard RemoveContext) error {
	if strings.TrimSpace(path) == "" || strings.TrimSpace(guard.AllowedRoot) == "" {
		return fmt.Errorf("safe remove requires a path and allowed root")
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve removal path %q: %w", path, err)
	}
	root, err := filepath.EvalSymlinks(guard.AllowedRoot)
	if err != nil {
		return fmt.Errorf("resolve allowed root %q: %w", guard.AllowedRoot, err)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(target))
	if err != nil {
		return fmt.Errorf("resolve removal parent %q: %w", path, err)
	}
	resolved := filepath.Join(parent, filepath.Base(target))
	if !within(root, resolved) {
		return fmt.Errorf("refuse to remove %q outside allowed root %q", path, guard.AllowedRoot)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	for _, protected := range append([]string{string(filepath.Separator), cwd, home}, guard.Protected...) {
		if protected == "" {
			continue
		}
		protected, err = filepath.Abs(protected)
		if err != nil {
			return err
		}
		if !within(protected, resolved) && !within(resolved, protected) && resolved != protected {
			continue
		}
		if protected == cwd || protected == home || protected == string(filepath.Separator) {
			if resolved != protected && !within(resolved, protected) {
				continue
			}
		}
		return fmt.Errorf("refuse to remove %q: overlaps protected path %q", path, protected)
	}
	return os.RemoveAll(target)
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
