package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
	"golang.org/x/mod/sumdb/dirhash"
	modzip "golang.org/x/mod/zip"
)

const sdkPrefix = "github.com/flanksource/telltale/sdk"

type releaseModule struct {
	dir, path string
	file      *modfile.File
	deps      []string
	sums      []string
	state     int
}

func main() {
	if len(os.Args) != 2 || !semver.IsValid(os.Args[1]) || semver.Canonical(os.Args[1]) != os.Args[1] {
		fmt.Fprintln(os.Stderr, "usage: modsum vX.Y.Z")
		os.Exit(1)
	}
	if err := run(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(version string) (err error) {
	var dirs = []string{"."}
	if err := filepath.WalkDir("sdk", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && entry.Name() == "go.mod" {
			dirs = append(dirs, filepath.Dir(path))
		}
		return nil
	}); err != nil {
		return err
	}
	modules := make(map[string]*releaseModule)
	var root *releaseModule
	for _, dir := range dirs {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err != nil {
			return err
		}
		file, err := modfile.Parse(filepath.Join(dir, "go.mod"), data, nil)
		if err != nil {
			return err
		}
		if file.Module == nil {
			return fmt.Errorf("%s has no module directive", dir)
		}
		m := &releaseModule{dir: dir, path: file.Module.Mod.Path, file: file}
		if dir == "." {
			root = m
			continue
		}
		expected := "github.com/flanksource/telltale/" + filepath.ToSlash(dir)
		if m.path != expected {
			return fmt.Errorf("%s: module path %s, expected %s", dir, m.path, expected)
		}
		modules[m.path] = m
	}
	if modules[sdkPrefix] == nil {
		return fmt.Errorf("sdk/go.mod is missing")
	}
	all := append([]*releaseModule{root}, mapValues(modules)...)
	for _, m := range all {
		for _, req := range m.file.Require {
			if req.Mod.Path == sdkPrefix || strings.HasPrefix(req.Mod.Path, sdkPrefix+"/") {
				if modules[req.Mod.Path] == nil {
					return fmt.Errorf("%s requires undiscovered module %s", m.dir, req.Mod.Path)
				}
				m.deps = append(m.deps, req.Mod.Path)
				if err := m.file.AddRequire(req.Mod.Path, version); err != nil {
					return err
				}
			}
		}
		for _, replacement := range m.file.Replace {
			if replacement.Old.Path == sdkPrefix || strings.HasPrefix(replacement.Old.Path, sdkPrefix+"/") {
				if err := m.file.DropReplace(replacement.Old.Path, replacement.Old.Version); err != nil {
					return err
				}
			}
		}
		data, err := m.file.Format()
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(m.dir, "go.mod"), data, 0644); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(".tmp", 0755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(".tmp", "modsum-")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(tmp)) }()
	tmp, err = filepath.Abs(tmp)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module example.invalid/modsum\n\ngo 1.27.1\n"), 0644); err != nil {
		return err
	}
	tracked, err := exec.Command("git", "ls-files", "-z", "--", "sdk").Output()
	if err != nil {
		return err
	}
	for _, path := range strings.Split(string(tracked), "\x00") {
		if path == "" {
			continue
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported tracked file %s", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		target := filepath.Join(tmp, path)
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0644); err != nil {
			return err
		}
	}
	var visit func(*releaseModule) error
	visit = func(m *releaseModule) error {
		if m.state == 2 {
			return nil
		}
		if m.state == 1 {
			return fmt.Errorf("cyclic SDK module dependencies at %s", m.path)
		}
		m.state = 1
		for _, dep := range m.deps {
			if err := visit(modules[dep]); err != nil {
				return err
			}
		}
		lines := map[string]bool{}
		var collect func(*releaseModule)
		collect = func(dep *releaseModule) {
			for _, line := range dep.sums {
				lines[line] = true
			}
			for _, child := range dep.deps {
				collect(modules[child])
			}
		}
		for _, dep := range m.deps {
			collect(modules[dep])
		}
		if err := updateSums(m.dir, version, lines); err != nil {
			return err
		}
		if m != root {
			sum, err := os.ReadFile(filepath.Join(m.dir, "go.sum"))
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			if err == nil {
				if err := os.WriteFile(filepath.Join(tmp, m.dir, "go.sum"), sum, 0644); err != nil {
					return err
				}
			}
			zipPath := filepath.Join(tmp, "module.zip")
			f, err := os.Create(zipPath)
			if err != nil {
				return err
			}
			err = modzip.CreateFromDir(f, module.Version{Path: m.path, Version: version}, filepath.Join(tmp, m.dir))
			closeErr := f.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
			zipSum, err := dirhash.HashZip(zipPath, dirhash.Hash1)
			if err != nil {
				return err
			}
			modPath := filepath.Join(m.dir, "go.mod")
			modSum, err := dirhash.Hash1([]string{"go.mod"}, func(string) (io.ReadCloser, error) { return os.Open(modPath) })
			if err != nil {
				return err
			}
			m.sums = []string{m.path + " " + version + " " + zipSum, m.path + " " + version + "/go.mod " + modSum}
		}
		m.state = 2
		return nil
	}
	for _, m := range all {
		if err := visit(m); err != nil {
			return err
		}
	}
	return nil
}

func mapValues(modules map[string]*releaseModule) []*releaseModule {
	paths := make([]string, 0, len(modules))
	for path := range modules {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	result := make([]*releaseModule, 0, len(paths))
	for _, path := range paths {
		result = append(result, modules[path])
	}
	return result
}

func updateSums(dir, version string, additions map[string]bool) error {
	path := filepath.Join(dir, "go.sum")
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 3 {
			return fmt.Errorf("invalid checksum line in %s: %q", path, line)
		}
		if (fields[0] == sdkPrefix || strings.HasPrefix(fields[0], sdkPrefix+"/")) && (fields[1] == version || fields[1] == version+"/go.mod") {
			continue
		}
		additions[line] = true
	}
	if len(additions) == 0 {
		return nil
	}
	lines := make([]string, 0, len(additions))
	for line := range additions {
		lines = append(lines, line)
	}
	sort.Strings(lines)
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0644)
}
