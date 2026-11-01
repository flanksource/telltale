package definition

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/flanksource/commons-db/connection/store"

	"github.com/flanksource/telltale"
)

// maxBody bounds a definition request's body.
const maxBody = 1 << 20

// Service serves a definition store over HTTP and arms the runs a start
// request asks for (telltale.Definitions), mounted under a telltale.Handler:
//
//	GET    /definitions           every definition in effect ([]Summary)
//	GET    /definitions/schema    the CRD's openAPIV3Schema, every typed list and the layers (SchemaView)
//	POST   /definitions/validate  {definition}: its static errors and its entries (Validation)
//	GET    /definitions/{name}    the definition in effect (Detail)
//	PUT    /definitions/{name}    {layer, resourceVersion, definition}: create ("") or replace it in layer
//	DELETE /definitions/{name}    ?layer=&resourceVersion=: remove it from layer
type Service struct {
	store       *Store
	connections *store.Store
}

var _ telltale.Definitions = (*Service)(nil)

// NewService serves defs, resolving default connections in connections.
func NewService(defs *Store, connections *store.Store) (*Service, error) {
	if defs == nil || connections == nil {
		return nil, errors.New("trace definitions service: a definition store and a connection store are required")
	}
	return &Service{store: defs, connections: connections}, nil
}

func (s *Service) options() Options { return Options{Connections: s.connections} }

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rest, ok := strings.CutPrefix(r.URL.Path, "/definitions")
	switch {
	case !ok || (rest != "" && !strings.HasPrefix(rest, "/")):
		writeError(w, http.StatusNotFound, fmt.Errorf("no definitions route %s", r.URL.Path))
	case rest == "":
		allow(w, r, http.MethodGet, s.list)
	case rest == "/schema" && r.Method == http.MethodGet:
		s.schema(w)
	case rest == "/validate" && r.Method == http.MethodPost:
		s.validate(w, r)
	default:
		name := strings.TrimPrefix(rest, "/")
		if name == "" || strings.Contains(name, "/") {
			writeError(w, http.StatusNotFound, fmt.Errorf("name one definition: /definitions/<name>, not %s", r.URL.Path))
			return
		}
		s.serveNamed(w, r, name)
	}
}

func (s *Service) serveNamed(w http.ResponseWriter, r *http.Request, name string) {
	switch r.Method {
	case http.MethodGet:
		s.get(w, name)
	case http.MethodPut:
		s.put(w, r, name)
	case http.MethodDelete:
		s.delete(w, r, name)
	default:
		w.Header().Set("Allow", "GET, PUT, DELETE")
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method %s not allowed", r.Method))
	}
}

func allow(w http.ResponseWriter, r *http.Request, method string, serve func(http.ResponseWriter)) {
	if r.Method != method {
		w.Header().Set("Allow", method)
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method %s not allowed", r.Method))
		return
	}
	serve(w)
}

func (s *Service) list(w http.ResponseWriter) {
	all, err := s.store.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, Summaries(all))
}

func (s *Service) schema(w http.ResponseWriter) {
	view, err := s.Schema()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Service) validate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Definition json.RawMessage `json:"definition"`
	}
	def, err := decodeBody(w, r, &body, func() json.RawMessage { return body.Definition })
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, Check(def, s.options()))
}

func (s *Service) get(w http.ResponseWriter, name string) {
	stored, err := s.store.Get(name)
	switch {
	case errors.Is(err, os.ErrNotExist):
		writeError(w, http.StatusNotFound, err)
	case err != nil:
		writeError(w, http.StatusUnprocessableEntity, err)
	default:
		writeJSON(w, http.StatusOK, Describe(stored, s.options()))
	}
}

func (s *Service) put(w http.ResponseWriter, r *http.Request, name string) {
	var body struct {
		Layer           string          `json:"layer"`
		ResourceVersion string          `json:"resourceVersion"`
		Definition      json.RawMessage `json:"definition"`
	}
	def, err := decodeBody(w, r, &body, func() json.RawMessage { return body.Definition })
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if def.Name != name {
		writeJSON(w, http.StatusBadRequest, &ValidationError{Name: name, Fields: []telltale.FieldError{{
			InstancePath: "/metadata/name", Message: fmt.Sprintf("%q is not %q, the definition this request puts", def.Name, name),
		}}})
		return
	}
	saved, err := s.store.Put(body.Layer, def, body.ResourceVersion, s.options())
	if err != nil {
		writeStoreError(w, name, err)
		return
	}
	writeJSON(w, http.StatusOK, Describe(saved, s.options()))
}

func (s *Service) delete(w http.ResponseWriter, r *http.Request, name string) {
	query := r.URL.Query()
	if err := s.store.Delete(query.Get("layer"), name, query.Get("resourceVersion")); err != nil {
		writeStoreError(w, name, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeStoreError answers a refused Put or Delete: 409 with the current
// resourceVersion for a conflict, 400 for an invalid definition or a layer
// that cannot be written, 404 for a definition the layer does not have.
func writeStoreError(w http.ResponseWriter, name string, err error) {
	var conflict *ConflictError
	var invalid *ValidationError
	switch {
	case errors.As(err, &conflict):
		writeJSON(w, http.StatusConflict, map[string]string{"error": conflict.Error(), "resourceVersion": conflict.Got})
	case errors.As(err, &invalid):
		writeJSON(w, http.StatusBadRequest, invalid)
	case errors.Is(err, ErrLayer):
		writeJSON(w, http.StatusBadRequest, &ValidationError{Name: name, Fields: []telltale.FieldError{{InstancePath: "/layer", Message: err.Error()}}})
	case errors.Is(err, os.ErrNotExist):
		writeError(w, http.StatusNotFound, err)
	default:
		writeError(w, http.StatusInternalServerError, err)
	}
}

// decodeBody reads r's JSON body into body and decodes the definition it
// holds: a TraceDefinition as JSON, or its YAML as a string.
func decodeBody(w http.ResponseWriter, r *http.Request, body any, definition func() json.RawMessage) (*TraceDefinition, error) {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(body); err != nil {
		return nil, fmt.Errorf("decode request: %w", err)
	}
	raw := definition()
	if len(raw) == 0 {
		return nil, errors.New("decode request: a definition is required")
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		raw = []byte(text)
	}
	return Decode(raw)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		panic(http.ErrAbortHandler)
	}
}
