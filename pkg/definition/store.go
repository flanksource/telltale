package definition

import (
	"cmp"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/flanksource/telltale/pkg/datadir"
)

// Dir is where a config dir keeps its definitions, one <name>.yaml each.
const Dir = "traces"

// LayerBuiltin is the read-only layer of the definitions telltale ships.
const LayerBuiltin = "builtin"

//go:embed builtin/*.yaml
var builtin embed.FS

// ErrConflict is a Put or Delete against a resourceVersion the file no
// longer has (or a create of a file that exists): it was changed since read.
var ErrConflict = errors.New("trace definition conflict")

// ConflictError is an ErrConflict naming what was expected and found.
type ConflictError struct {
	Name, Layer string
	// Want is the resourceVersion the caller read, empty for a create; Got is
	// the file's now, empty when it no longer exists.
	Want, Got string
}

func (e *ConflictError) Error() string {
	switch {
	case e.Want == "":
		return fmt.Sprintf("trace definition %s already exists in %s (resourceVersion %s): pass its resourceVersion to replace it", e.Name, e.Layer, e.Got)
	case e.Got == "":
		return fmt.Sprintf("trace definition %s no longer exists in %s", e.Name, e.Layer)
	}
	return fmt.Sprintf("trace definition %s in %s is at resourceVersion %s, not %s: it changed since it was read", e.Name, e.Layer, e.Got, e.Want)
}

func (e *ConflictError) Unwrap() error { return ErrConflict }

// Stored is a definition as a store holds it.
type Stored struct {
	Name  string `json:"name"`
	Layer string `json:"layer"`
	Path  string `json:"path"`
	// ResourceVersion is the hash of the file's content; a Put of it must
	// name it. It is also set on Definition.ResourceVersion.
	ResourceVersion string           `json:"resourceVersion"`
	Definition      *TraceDefinition `json:"definition,omitempty"`
	// Problem is why the file defines no usable definition; Definition is
	// then nil, and Get refuses it.
	Problem string `json:"problem,omitempty"`
	// Data is the file's content.
	Data []byte `json:"-"`
}

// LayerFile is the layer of a definition read from a file outside the store.
const LayerFile = "file"

// LoadFile is the definition in the file at path, which must be named by its
// metadata.name, as a store's are.
func LoadFile(path string) (Stored, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Stored{}, fmt.Errorf("read trace definition: %w", err)
	}
	loaded := stored(LayerFile, path, data)
	if loaded.Problem != "" {
		return Stored{}, fmt.Errorf("trace definition %s: %s", path, loaded.Problem)
	}
	return loaded, nil
}

// Layer is one of a store's layers: its name, whether it is written to, and
// the dir it keeps its files in, empty for the built-ins.
type Layer struct {
	Name     string `json:"name"`
	Writable bool   `json:"writable"`
	Dir      string `json:"dir,omitempty"`
}

// LayerDirs are the store's layers in override order, the built-ins last.
func (s *Store) LayerDirs() []Layer {
	layers := make([]Layer, 0, len(s.dirs)+1)
	for _, dir := range s.dirs {
		layers = append(layers, Layer{Name: dir.Name, Writable: true, Dir: filepath.Join(dir.Dir, Dir)})
	}
	return append(layers, Layer{Name: LayerBuiltin})
}

// Store is the definitions of the config dirs, <dir>/traces/<name>.yaml, the
// first layer to have a name winning, over the built-in ones. It reads the
// files on every call, so an edit applies at once.
type Store struct {
	dirs []datadir.ConfigDir
}

// OpenStore is the store over datadir.ConfigDirs().
func OpenStore() (*Store, error) {
	dirs, err := datadir.ConfigDirs()
	if err != nil {
		return nil, err
	}
	return NewStore(dirs), nil
}

// NewStore is the store over dirs, in override order, then the built-ins.
func NewStore(dirs []datadir.ConfigDir) *Store { return &Store{dirs: slices.Clone(dirs)} }

// Layers are the store's layer names, in override order.
func (s *Store) Layers() []string {
	names := make([]string, 0, len(s.dirs)+1)
	for _, dir := range s.dirs {
		names = append(names, dir.Name)
	}
	return append(names, LayerBuiltin)
}

// List is every definition in effect, each its first layer's, by name.
func (s *Store) List() ([]Stored, error) {
	effective := map[string]Stored{}
	for _, layer := range s.Layers() {
		read, err := s.readLayer(layer)
		if err != nil {
			return nil, err
		}
		for _, stored := range read {
			if _, shadowed := effective[stored.Name]; !shadowed {
				effective[stored.Name] = stored
			}
		}
	}
	all := make([]Stored, 0, len(effective))
	for _, stored := range effective {
		all = append(all, stored)
	}
	slices.SortFunc(all, func(a, b Stored) int { return cmp.Compare(a.Name, b.Name) })
	return all, nil
}

// Get is the definition name in effect; one no layer has is os.ErrNotExist.
func (s *Store) Get(name string) (Stored, error) {
	if err := validName(name); err != nil {
		return Stored{}, fmt.Errorf("get trace definition: %w", err)
	}
	for _, layer := range s.Layers() {
		stored, found, err := s.read(layer, name)
		if err != nil {
			return Stored{}, err
		}
		if !found {
			continue
		}
		if stored.Problem != "" {
			return Stored{}, fmt.Errorf("trace definition %s (%s): %s", name, stored.Path, stored.Problem)
		}
		return stored, nil
	}
	return Stored{}, fmt.Errorf("trace definition %s in %v: %w", name, s.Layers(), os.ErrNotExist)
}

// Put writes def to layer as <name>.yaml, refusing an invalid definition and,
// with an ErrConflict, a resourceVersion that is not the file's: empty
// creates a file that must not exist, else it must be the version read.
func (s *Store) Put(layer string, def *TraceDefinition, resourceVersion string, options Options) (Stored, error) {
	dir, err := s.writable(layer)
	if err != nil {
		return Stored{}, err
	}
	if err := Validate(def, options); err != nil {
		return Stored{}, err
	}
	current, found, err := s.read(layer, def.Name)
	if err != nil {
		return Stored{}, err
	}
	if (resourceVersion == "") == found || (found && current.ResourceVersion != resourceVersion) {
		return Stored{}, &ConflictError{Name: def.Name, Layer: layer, Want: resourceVersion, Got: current.ResourceVersion}
	}
	saved := *def
	saved.APIVersion, saved.Kind, saved.ResourceVersion = APIVersion, Kind, ""
	data, err := Encode(&saved)
	if err != nil {
		return Stored{}, err
	}
	path := filepath.Join(dir, def.Name+".yaml")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Stored{}, fmt.Errorf("put trace definition %s: %w", def.Name, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return Stored{}, fmt.Errorf("put trace definition %s: %w", def.Name, err)
	}
	return stored(layer, path, data), nil
}

// Delete removes name's file from layer, refusing a resourceVersion it no
// longer has with an ErrConflict; one the layer does not have is
// os.ErrNotExist.
func (s *Store) Delete(layer, name, resourceVersion string) error {
	if _, err := s.writable(layer); err != nil {
		return err
	}
	current, found, err := s.read(layer, name)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("delete trace definition %s from %s: %w", name, layer, os.ErrNotExist)
	}
	if current.ResourceVersion != resourceVersion {
		return &ConflictError{Name: name, Layer: layer, Want: resourceVersion, Got: current.ResourceVersion}
	}
	if err := os.Remove(current.Path); err != nil {
		return fmt.Errorf("delete trace definition %s: %w", name, err)
	}
	return nil
}

// writable is layer's definitions dir, refusing the built-in layer and one
// the store does not have.
func (s *Store) writable(layer string) (string, error) {
	for _, dir := range s.dirs {
		if dir.Name == layer {
			return filepath.Join(dir.Dir, Dir), nil
		}
	}
	if layer == LayerBuiltin {
		return "", fmt.Errorf("%w: the %s trace definitions are read-only: put a definition of the same name in %v to override one", ErrLayer, LayerBuiltin, s.Layers()[:len(s.dirs)])
	}
	return "", fmt.Errorf("%w: no trace definition layer %q among %v", ErrLayer, layer, s.Layers())
}

// ErrLayer is a write to a layer that cannot take one: the built-ins, or one
// the store does not have.
var ErrLayer = errors.New("trace definition layer refused")

// layerFS is layer's definitions dir as a file system and the path its files
// are reported under.
func (s *Store) layerFS(layer string) (fs.FS, string, error) {
	if layer == LayerBuiltin {
		sub, err := fs.Sub(builtin, LayerBuiltin)
		return sub, LayerBuiltin + ":", err
	}
	dir, err := s.writable(layer)
	return os.DirFS(dir), dir, err
}

func (s *Store) readLayer(layer string) ([]Stored, error) {
	files, root, err := s.layerFS(layer)
	if err != nil {
		return nil, err
	}
	entries, err := fs.ReadDir(files, ".")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s trace definitions in %s: %w", layer, root, err)
	}
	var read []Stored
	for _, entry := range entries {
		name, isDefinition := strings.CutSuffix(entry.Name(), ".yaml")
		if entry.IsDir() || !isDefinition || strings.HasPrefix(name, ".") {
			continue
		}
		stored, _, err := s.read(layer, name)
		if err != nil {
			return nil, err
		}
		read = append(read, stored)
	}
	return read, nil
}

// read is name's file in layer, found false when the layer has none. A file
// that is no usable definition is still read, carrying its Problem.
func (s *Store) read(layer, name string) (Stored, bool, error) {
	files, root, err := s.layerFS(layer)
	if err != nil {
		return Stored{}, false, err
	}
	data, err := fs.ReadFile(files, name+".yaml")
	if errors.Is(err, fs.ErrNotExist) {
		return Stored{}, false, nil
	}
	if err != nil {
		return Stored{}, false, fmt.Errorf("read trace definition %s: %w", name, err)
	}
	return stored(layer, filepath.Join(root, name+".yaml"), data), true, nil
}

// stored is the file at path of layer with content data.
func stored(layer, path string, data []byte) Stored {
	name := strings.TrimSuffix(filepath.Base(path), ".yaml")
	sum := sha256.Sum256(data)
	stored := Stored{Name: name, Layer: layer, Path: path, ResourceVersion: hex.EncodeToString(sum[:8]), Data: data}
	def, err := Decode(data)
	switch {
	case err != nil:
		stored.Problem = err.Error()
	case def.Name != name:
		stored.Problem = fmt.Sprintf("metadata.name %q is not its file name %q", def.Name, name)
	default:
		def.ResourceVersion = stored.ResourceVersion
		stored.Definition = def
	}
	return stored
}
