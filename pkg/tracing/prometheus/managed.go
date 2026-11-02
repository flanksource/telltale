package prometheus

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/flanksource/commons-db/models"
	"github.com/flanksource/deps/start"
	"github.com/google/uuid"
)

// Version is the Prometheus release the managed Prometheus runs: a 3.x, whose
// targets take their scrape interval and timeout from their own labels.
const Version = "3.5.0"

// Job is the managed Prometheus's job that scrapes the file_sd targets.
const Job = "file_sd"

// ConnectionName names the managed Prometheus's connection.
const ConnectionName = "telltale-prometheus"

const service = "prometheus"

// ManagedPaths are where a data dir keeps its managed Prometheus: the deps
// service state (its TSDB under <State>/prometheus/data), and the file_sd
// target files its captures write.
type ManagedPaths struct {
	State  string
	FileSD string
}

// Paths are the managed Prometheus's absolute paths under dataDir: deps takes
// only an absolute file_sd dir, and the state path keys the connection's ID.
func Paths(dataDir string) (ManagedPaths, error) {
	root, err := filepath.Abs(dataDir)
	if err != nil {
		return ManagedPaths{}, fmt.Errorf("resolve the data dir %q: %w", dataDir, err)
	}
	return ManagedPaths{State: filepath.Join(root, "services"), FileSD: filepath.Join(root, "prometheus", "targets")}, nil
}

// Managed is the Prometheus of a data dir, one per data dir: the first
// capture of a run starts it through deps start, and every later one reuses
// it with its TSDB. deps supervises a binary in the process that starts it,
// so it is telltale's sub-process: Release stops it once no run of this
// process holds it, and the next capture starts it again on the same TSDB and
// port. A Prometheus another process supervises is reused and left running.
type Managed struct {
	dataDir string
	mu      sync.Mutex
	held    *lease
}

// lease is one data dir's Prometheus, shared by every run of this process
// that captures into that data dir.
type lease struct {
	state    string
	instance *start.Instance
	owned    bool
	holders  int
}

var leases = struct {
	sync.Mutex
	byState map[string]*lease
}{byState: map[string]*lease{}}

// NewManaged is the managed Prometheus of dataDir, for one run.
func NewManaged(dataDir string) *Managed { return &Managed{dataDir: dataDir} }

// Server is the run's Prometheus, started on the run's first call.
func (m *Managed) Server(ctx context.Context) (Server, error) {
	held, paths, err := m.hold(ctx)
	if err != nil {
		return Server{}, err
	}
	return Server{URL: held.instance.Connection.URL, Job: Job, FileSDDir: paths.FileSD}, nil
}

// Connection is the run's Prometheus as the read-only connection its samples
// are queried through, started on the run's first call as Server is, so a run
// that only queries it holds it too.
func (m *Managed) Connection(ctx context.Context) (*models.Connection, error) {
	held, paths, err := m.hold(ctx)
	if err != nil {
		return nil, err
	}
	return managedConnection(held.instance.Connection, paths), nil
}

func (m *Managed) hold(ctx context.Context) (*lease, ManagedPaths, error) {
	if m.dataDir == "" {
		return nil, ManagedPaths{}, errors.New("a managed prometheus lives in telltale's data dir, and none is set")
	}
	paths, err := Paths(m.dataDir)
	if err != nil {
		return nil, ManagedPaths{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.held == nil {
		if m.held, err = acquire(ctx, paths); err != nil {
			return nil, ManagedPaths{}, err
		}
	}
	return m.held, paths, nil
}

// Release ends the run's hold on its Prometheus, stopping a Prometheus this
// process supervises once no other run holds it.
func (m *Managed) Release(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	held := m.held
	if held == nil {
		return nil
	}
	m.held = nil
	leases.Lock()
	defer leases.Unlock()
	if held.holders--; held.holders > 0 {
		return nil
	}
	delete(leases.byState, held.state)
	if !held.owned {
		return nil
	}
	if err := held.instance.Stop(ctx); err != nil {
		return fmt.Errorf("stop the managed prometheus of %s: %w", held.state, err)
	}
	return nil
}

func acquire(ctx context.Context, paths ManagedPaths) (*lease, error) {
	leases.Lock()
	defer leases.Unlock()
	if held, ok := leases.byState[paths.State]; ok {
		held.holders++
		return held, nil
	}
	instance, err := startManaged(ctx, paths)
	if err != nil {
		return nil, err
	}
	held := &lease{state: paths.State, instance: instance, owned: instance.State.SupervisorPID == os.Getpid(), holders: 1}
	leases.byState[paths.State] = held
	return held, nil
}

func startManaged(ctx context.Context, paths ManagedPaths) (*start.Instance, error) {
	if err := os.MkdirAll(paths.FileSD, 0o755); err != nil {
		return nil, fmt.Errorf("create the file_sd dir: %w", err)
	}
	options := []start.Option{
		start.WithStateDir(paths.State), start.WithRuntime(start.RuntimeBinary), start.WithVersion(Version),
		start.WithParameters(map[string]string{"file-sd-dir": paths.FileSD}),
	}
	_, err := start.Get(ctx, service, start.WithStateDir(paths.State))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// The first start takes a free port; later ones keep the port its state recorded.
		port, err := freePort()
		if err != nil {
			return nil, err
		}
		options = append(options, start.WithPort(port))
	case err != nil:
		return nil, fmt.Errorf("read the managed prometheus state in %s: %w", paths.State, err)
	}
	return start.Start(ctx, service, options...)
}

func freePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("find a free port for the managed prometheus: %w", err)
	}
	defer func() { _ = listener.Close() }()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

// ManagedConnection is the connection of dataDir's managed Prometheus, as a
// read-only connection its samples are queried through; nil when no capture
// has started one. It answers while a capture's run holds that Prometheus.
func ManagedConnection(ctx context.Context, dataDir string) (*models.Connection, error) {
	paths, err := Paths(dataDir)
	if err != nil {
		return nil, err
	}
	instance, err := start.Get(ctx, service, start.WithStateDir(paths.State))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the managed prometheus state in %s: %w", paths.State, err)
	}
	return managedConnection(instance.Connection, paths), nil
}

func managedConnection(connection models.Connection, paths ManagedPaths) *models.Connection {
	connection.ID = ManagedConnectionID(paths)
	connection.Name, connection.Namespace = ConnectionName, ""
	connection.Source = filepath.Join(paths.State, service, "state.yaml")
	connection.ReadOnly, connection.Virtual = true, true
	return &connection
}

// ManagedConnectionID is the id of the managed connection kept at paths.
func ManagedConnectionID(paths ManagedPaths) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("telltale://managed/prometheus/"+paths.State))
}

// NamesManaged reports whether reference names the managed connection kept at
// paths: by its id, its name or connection://<name>.
func NamesManaged(paths ManagedPaths, reference string) bool {
	return reference == ManagedConnectionID(paths).String() || reference == ConnectionName || reference == "connection://"+ConnectionName
}
