package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/flanksource/clicky/entity"
	"github.com/flanksource/commons-db/cmd/query/connections"
	"github.com/flanksource/commons-db/cmd/query/secretcatalog"
	"github.com/flanksource/commons-db/connection/store"
	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/query"
)

func connectionHandler(catalog *store.Store) (http.Handler, error) {
	provider, err := connections.NewStoreProvider(connections.StoreProviderOptions{Store: catalog, WriteLayer: store.LayerCWD})
	if err != nil {
		return nil, err
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
