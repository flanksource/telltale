package traces

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/flanksource/clicky/cache"
	cachevalkey "github.com/flanksource/clicky/valkey"
	"github.com/flanksource/commons-db/cmd/query/sessionstore"
	"github.com/flanksource/commons-db/connection/store"
	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/models"
	"github.com/flanksource/commons-db/query"
	commonscontext "github.com/flanksource/commons/context"
	"github.com/flanksource/commons/logger"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/valkey-io/valkey-go"
)

// SessionStores opens the state namespace of a run. Nil leaves each run with
// its own in-memory store.
type SessionStores func(runID string) (query.SessionStore, error)

// OpenSessionStores connects to a configured Redis-compatible session store.
// The caller must close the client after its runs and read handlers have closed.
func OpenSessionStores(ctx context.Context, dataDir, reference string, connections *store.Store) (SessionStores, func(), error) {
	if reference == "" {
		return nil, func() {}, nil
	}
	if !strings.HasPrefix(reference, "connection://") || !dbcontext.IsValidConnectionURL(reference) {
		return nil, nil, errors.New("session store: expected a connection:// reference")
	}
	if connections == nil {
		return nil, nil, errors.New("session store: a connection catalog is required")
	}
	dbCtx := dbcontext.NewContext(ctx, commonscontext.WithLogger(logger.NewWithWriter(io.Discard)))
	connection, err := connections.ResolveConnection(dbCtx, reference)
	if err != nil || connection == nil {
		return nil, nil, errors.New("session store: could not resolve the configured connection")
	}
	if connection.Type != models.ConnectionTypeRedis {
		return nil, nil, errors.New("session store: the connection must be Redis or Valkey")
	}
	connection, err = dbcontext.HydrateConnection(connections.Attach(dbCtx), connection)
	if err != nil {
		return nil, nil, errors.New("session store: could not resolve the configured connection credentials")
	}
	options, err := valkey.ParseURL(connection.URL)
	if err != nil {
		return nil, nil, errors.New("session store: invalid Redis/Valkey connection URL")
	}
	if connection.Username != "" {
		options.Username = connection.Username
	}
	if connection.Password != "" {
		options.Password = connection.Password
	}
	return connectSessionStores(ctx, dataDir, options)
}

func connectSessionStores(ctx context.Context, dataDir string, options valkey.ClientOption) (SessionStores, func(), error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, nil, fmt.Errorf("session store: create data directory: %w", err)
	}
	id, err := sessionDataDirID(ctx, dataDir)
	if err != nil {
		return nil, nil, err
	}
	options.Dialer.Timeout = 5 * time.Second
	client, err := valkey.NewClient(options)
	if err != nil {
		return nil, nil, errors.New("session store: could not connect to Redis/Valkey")
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := client.Do(pingCtx, client.B().Ping().Build()).Error(); err != nil {
		client.Close()
		return nil, nil, errors.New("session store: Redis/Valkey did not answer PING")
	}
	return sessionStores(cachevalkey.NewStore(client), id), client.Close, nil
}

func sessionStores(backend cache.Store, dataDirID string) SessionStores {
	return func(runID string) (query.SessionStore, error) {
		if runID == "" || strings.Contains(runID, ":") {
			return nil, errors.New("session store: a run ID without colons is required")
		}
		prefix := "telltale:" + dataDirID + ":" + runID + ":"
		return sessionstore.NewKVStore(func(context.Context) (cache.Store, string, error) {
			return backend, prefix, nil
		}, sessionstore.KVStoreOptions{})
	}
}

func sessionDataDirID(ctx context.Context, dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("session store: resolve data directory: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("session store: resolve data directory: %w", err)
	}
	var machine string
	if runtime.GOOS == "linux" {
		// HostID can fall back to the boot ID on Linux. That would strand
		// sessions after a reboot, so require the persistent machine ID.
		data, err := os.ReadFile("/etc/machine-id")
		if err != nil {
			return "", fmt.Errorf("session store: read Linux machine ID: %w", err)
		}
		machine = strings.TrimSpace(string(data))
	} else {
		machine, err = host.HostIDWithContext(ctx)
		if err != nil {
			return "", fmt.Errorf("session store: read machine ID: %w", err)
		}
	}
	if machine == "" {
		return "", errors.New("session store: a stable machine ID is required")
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(machine+"\x00"+canonical))), nil
}
