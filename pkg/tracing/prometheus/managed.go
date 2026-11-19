package prometheus

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/flanksource/commons-db/models"
	"github.com/google/uuid"
)

const Version = "v1.156.0"
const Job = "file_sd"
const ConnectionName = "telltale-prometheus"

type ManagedPaths struct {
	State  string
	FileSD string
}

func Paths(dataDir string) (ManagedPaths, error) {
	root, err := filepath.Abs(dataDir)
	if err != nil {
		return ManagedPaths{}, fmt.Errorf("resolve the data dir %q: %w", dataDir, err)
	}
	return ManagedPaths{State: filepath.Join(root, "services"), FileSD: filepath.Join(root, "victoriametrics", "targets")}, nil
}

func (p ManagedPaths) serviceDir() string { return filepath.Join(p.State, "victoriametrics") }

// Managed holds the data directory's VictoriaMetrics for a run or HTTP server.
type Managed struct {
	dataDir string
	mu      sync.Mutex
	held    *lease
}

type lease struct {
	paths   ManagedPaths
	server  Server
	stop    func() error
	holders int
}

var leases = struct {
	sync.Mutex
	byState map[string]*lease
}{byState: map[string]*lease{}}

func NewManaged(dataDir string) *Managed { return &Managed{dataDir: dataDir} }

func (m *Managed) Server(ctx context.Context) (Server, error) {
	if m.dataDir == "" {
		return Server{}, errors.New("managed VictoriaMetrics requires a telltale data directory")
	}
	paths, err := Paths(m.dataDir)
	if err != nil {
		return Server{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.held == nil {
		m.held, err = acquire(ctx, paths)
		if err != nil {
			return Server{}, err
		}
	}
	return m.held.server, nil
}

func (m *Managed) Connection(ctx context.Context) (*models.Connection, error) {
	server, err := m.Server(ctx)
	if err != nil {
		return nil, err
	}
	paths, err := Paths(m.dataDir)
	if err != nil {
		return nil, err
	}
	return managedConnection(server.URL, paths), nil
}

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
	delete(leases.byState, held.paths.State)
	if held.stop == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	unlock, err := lockInstance(ctx, held.paths)
	if err != nil {
		return errors.Join(err, held.stop())
	}
	return errors.Join(held.stop(), unlock())
}

func acquire(ctx context.Context, paths ManagedPaths) (_ *lease, err error) {
	leases.Lock()
	defer leases.Unlock()
	if held := leases.byState[paths.State]; held != nil {
		if err := newClient(held.server).health(ctx); err != nil {
			return nil, err
		}
		held.holders++
		return held, nil
	}
	if err := os.MkdirAll(paths.serviceDir(), 0o700); err != nil {
		return nil, err
	}
	unlock, err := lockInstance(ctx, paths)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	held, err := openInstance(ctx, paths)
	if err != nil {
		return nil, err
	}
	held.holders = 1
	leases.byState[paths.State] = held
	return held, nil
}

// ManagedConnection looks up the virtual connection without starting the service.
func ManagedConnection(_ context.Context, dataDir string) (*models.Connection, error) {
	paths, err := Paths(dataDir)
	if err != nil {
		return nil, err
	}
	state, err := readInstance(paths)
	if err != nil || state == nil {
		return nil, err
	}
	return managedConnection(state.server(paths).URL, paths), nil
}

func managedConnection(url string, paths ManagedPaths) *models.Connection {
	return &models.Connection{
		ID: ManagedConnectionID(paths), Name: ConnectionName, Type: models.ConnectionTypePrometheus,
		URL: url, Source: stateFile(paths), ReadOnly: true, Virtual: true,
	}
}

func ManagedConnectionID(paths ManagedPaths) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("telltale://managed/prometheus/"+paths.State))
}

func NamesManaged(paths ManagedPaths, reference string) bool {
	return reference == ManagedConnectionID(paths).String() || reference == ConnectionName || reference == "connection://"+ConnectionName
}
