package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"

	"github.com/flanksource/clicky/entity"
	"github.com/flanksource/commons-db/cmd/query/connections"
	"github.com/flanksource/commons-db/cmd/query/secretcatalog"
	"github.com/flanksource/commons-db/connection/store"
	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/models"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/telltale/pkg/tracing/prometheus"
)

// managedProvider is the connection store's provider with the connections
// telltale manages in its data dir, such as the managed Prometheus of the
// prometheus trace kind, listed and resolved as virtual, read-only
// connections the service refuses to edit. Writes go to the store.
type managedProvider struct {
	*connections.StoreProvider
	dataDir string
}

func (p managedProvider) managed() ([]*models.Connection, error) {
	connection, err := prometheus.ManagedConnection(context.Background(), p.dataDir)
	if err != nil || connection == nil {
		return nil, err
	}
	return []*models.Connection{connection}, nil
}

func (p managedProvider) ListConnections() ([]*models.Connection, error) {
	stored, err := p.StoreProvider.ListConnections()
	if err != nil {
		return nil, err
	}
	managed, err := p.managed()
	if err != nil {
		return nil, err
	}
	for _, connection := range managed {
		if index := slices.IndexFunc(stored, func(s *models.Connection) bool { return s.Name == connection.Name }); index >= 0 {
			return nil, fmt.Errorf("connection %q is defined in %s and also managed by telltale (%s); rename the file's connection", connection.Name, stored[index].Source, connection.Source)
		}
	}
	return append(stored, managed...), nil
}

func (p managedProvider) ResolveConnection(ctx dbcontext.Context, reference string) (*models.Connection, error) {
	connection, err := p.StoreProvider.ResolveConnection(ctx, reference)
	if err != nil || connection != nil {
		return connection, err
	}
	managed, err := p.managed()
	if err != nil {
		return nil, err
	}
	for _, connection := range managed {
		if reference == connection.ID.String() || reference == connection.Name || reference == "connection://"+connection.Name {
			return connection, nil
		}
	}
	return nil, nil
}

func connectionHandler(catalog *store.Store, dataDir string) (http.Handler, error) {
	stored, err := connections.NewStoreProvider(connections.StoreProviderOptions{Store: catalog, WriteLayer: store.LayerCWD})
	if err != nil {
		return nil, err
	}
	var provider connections.WritableConnectionProvider = stored
	if dataDir != "" {
		provider = managedProvider{StoreProvider: stored, dataDir: dataDir}
	}
	service, err := connections.New(connections.Options{
		Virtual:    provider,
		Secrets:    &secretcatalog.Options{},
		Context:    func() dbcontext.Context { return catalog.Attach(dbcontext.New()) },
		DecodeBody: func(_ context.Context, body map[string]any) (map[string]any, error) { return body, nil },
		Profiles:   func(context.Context) ([]query.Profile, error) { return []query.Profile{}, nil },
	})
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/connection", func(w http.ResponseWriter, r *http.Request) {
		value, err := service.List(connections.ListOptions{Type: r.URL.Query().Get("type"), Types: r.URL.Query().Get("types")})
		connectionResponse(w, value, err, http.StatusOK)
	})
	mux.HandleFunc("GET /api/v1/connection/{id}", func(w http.ResponseWriter, r *http.Request) {
		value, err := service.Get(r.PathValue("id"))
		connectionResponse(w, value, err, http.StatusOK)
	})
	mux.HandleFunc("POST /api/v1/connection", func(w http.ResponseWriter, r *http.Request) { saveConnection(w, r, service) })
	mux.HandleFunc("PUT /api/v1/connection/{id}", func(w http.ResponseWriter, r *http.Request) { saveConnection(w, r, service) })
	mux.HandleFunc("DELETE /api/v1/connection/{id}", func(w http.ResponseWriter, r *http.Request) {
		connectionResponse(w, nil, service.Delete(r.PathValue("id")), http.StatusNoContent)
	})
	return http.NewCrossOriginProtection().Handler(service.Handler("/api/v1", mux)), nil
}

func saveConnection(w http.ResponseWriter, r *http.Request, service *connections.Service) {
	var body map[string]any
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := decoder.Decode(&body); err != nil {
		entity.WriteError(w, entity.NewStatusErrorf(http.StatusBadRequest, "invalid_body", "expected a connection object: %v", err))
		return
	}
	if body == nil || decoder.Decode(new(any)) != io.EOF {
		entity.WriteError(w, entity.NewStatusError(http.StatusBadRequest, "invalid_body", "expected exactly one connection object"))
		return
	}
	for _, field := range []string{"name", "type"} {
		if value, ok := body[field].(string); !ok || value == "" {
			entity.WriteError(w, entity.NewStatusErrorf(http.StatusBadRequest, "invalid_body", "%s is required and must be a non-empty string", field))
			return
		}
	}
	if r.Method == http.MethodPost {
		value, err := service.Create(r.Context(), body)
		connectionResponse(w, value, err, http.StatusCreated)
	} else {
		value, err := service.Update(r.Context(), r.PathValue("id"), body)
		connectionResponse(w, value, err, http.StatusOK)
	}
}

func connectionResponse(w http.ResponseWriter, value any, err error, status int) {
	if err != nil {
		entity.WriteError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if status != http.StatusNoContent {
		if err := json.NewEncoder(w).Encode(value); err != nil {
			panic(fmt.Errorf("encode connection response: %w", err))
		}
	}
}
