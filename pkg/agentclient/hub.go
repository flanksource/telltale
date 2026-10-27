package agentclient

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/flanksource/telltale/pkg/credentials/apitoken"
	telltale "github.com/flanksource/telltale/sdk"
	"github.com/flanksource/telltale/sdk/protocol"
	"github.com/gorilla/websocket"
)

type Hub struct {
	registry *Registry
	verifier *apitoken.Verifier
}

func NewHub(registry *Registry, store *TokenStore) *Hub {
	return &Hub{registry: registry, verifier: apitoken.NewVerifier(store.Lookup)}
}

func (h *Hub) List(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := h.registry.Discover(r.Context(), ""); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	hellos := make([]protocol.Hello, 0)
	for _, agent := range h.registry.Match(nil) {
		hellos = append(hellos, agent.Hello)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(hellos)
}

func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.Authorize(apitoken.ScopeAgent, http.HandlerFunc(h.connect)).ServeHTTP(w, r)
}

func (h *Hub) Authorize(scope apitoken.Scope, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := apitoken.BearerFromHeader(r.Header.Get("Authorization"))
		if !ok {
			hubError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		record, err := h.verifier.Verify(r.Context(), token)
		if err != nil {
			if errors.Is(err, apitoken.ErrMalformed) || errors.Is(err, apitoken.ErrUnknown) || errors.Is(err, apitoken.ErrRevoked) || errors.Is(err, apitoken.ErrExpired) {
				hubError(w, http.StatusUnauthorized, "unauthorized")
			} else {
				log.Printf("agent token verification failed: %v", err)
				hubError(w, http.StatusInternalServerError, "token verification failed")
			}
			return
		}
		if record.Scope != scope && (scope != apitoken.ScopeAgent || record.Scope != apitoken.ScopeAPI) {
			hubError(w, http.StatusForbidden, "forbidden")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func hubError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func (h *Hub) connect(w http.ResponseWriter, r *http.Request) {
	token, _ := apitoken.BearerFromHeader(r.Header.Get("Authorization"))
	conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
	if err != nil {
		log.Printf("agent hub upgrade failed: %v", err)
		return
	}
	conn.SetReadLimit(protocol.MaxFrameSize)
	stream := &telltale.WebSocketStream{Conn: conn}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	agent, err := Connect(ctx, stream, token)
	cancel()
	if err != nil {
		log.Printf("agent hub connect failed: %v", err)
		_ = stream.Close()
		return
	}
	if err := h.registry.addHub(agent); err != nil {
		log.Printf("agent hub registration failed: %v", err)
		_ = agent.Close()
		return
	}
	<-agent.done
}
