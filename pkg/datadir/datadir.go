// Package datadir resolves where telltale keeps its data: trace data under
// the cache root (Cache), config and credentials in the config dirs
// (ConfigDirs): the working directory's .telltale, then the global Config.
package datadir

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/flanksource/commons-db/connection/store"
)

// App names telltale's dir under the cache and config roots.
const App = "telltale"

// Cache is telltale's cache root: $XDG_CACHE_HOME/telltale when that is set,
// otherwise os.UserCacheDir()/telltale.
func Cache() (string, error) {
	if dir := os.Getenv("XDG_CACHE_HOME"); dir != "" {
		return filepath.Join(dir, App), nil
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve the %s cache dir (set XDG_CACHE_HOME or --data-dir): %w", App, err)
	}
	return filepath.Join(dir, App), nil
}

// Config is telltale's config dir: $XDG_CONFIG_HOME/telltale when that is
// set, otherwise ~/.config/telltale.
func Config() (string, error) {
	dir, err := store.ConfigDir(App)
	if err != nil {
		return "", fmt.Errorf("resolve the %s config dir (set XDG_CONFIG_HOME): %w", App, err)
	}
	return dir, nil
}

// Config dir names, in the order ConfigDirs gives them.
const (
	ConfigCWD    = "cwd"
	ConfigGlobal = "global"
)

// ConfigDir is a directory telltale reads config and credentials from.
type ConfigDir struct {
	Name string
	Dir  string
}

// ConfigDirs are where telltale's config lives, the first to have a file
// winning: the working directory's .telltale, then the global Config dir.
func ConfigDirs() ([]ConfigDir, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("resolve the %s config dirs: %w", App, err)
	}
	global, err := Config()
	if err != nil {
		return nil, err
	}
	return []ConfigDir{{Name: ConfigCWD, Dir: filepath.Join(cwd, "."+App)}, {Name: ConfigGlobal, Dir: global}}, nil
}

// ConfigFile is name in the first of dirs that has it, or "" when none does.
func ConfigFile(dirs []ConfigDir, name string) (string, error) {
	for _, dir := range dirs {
		path := filepath.Join(dir.Dir, name)
		_, err := os.Stat(path)
		if err == nil {
			return path, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%s config file %s: %w", App, path, err)
		}
	}
	return "", nil
}

// Global is the dir of dirs named ConfigGlobal, where telltale writes config.
func Global(dirs []ConfigDir) (string, error) {
	for _, dir := range dirs {
		if dir.Name == ConfigGlobal {
			return dir.Dir, nil
		}
	}
	return "", fmt.Errorf("no %s config dir among %v", ConfigGlobal, dirs)
}
