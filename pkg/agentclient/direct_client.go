package agentclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/flanksource/telltale/sdk/protocol"
)

// AgentAPIVersion is the direct-HTTP agent API this client speaks: the routes
// and JWT scopes of telltale-diag.war and the attached agent's HTTP listener.
const AgentAPIVersion = 1

// ScopeRead is the scope of an agent's hello and catalog.
const ScopeRead = "jvm:read"

// TokenHeader carries the execution token. The Kubernetes apiserver deletes
// Authorization from every request it proxies (pods/proxy, services/proxy),
// so the token never travels in Authorization, nor in a query parameter.
const TokenHeader = "X-Telltale-Authorization"

// maxArtifact bounds a capture dump read from an agent.
const maxArtifact = 64 << 20

// TokenSource mints the short-lived bearer token one request presents.
type TokenSource interface {
	Token(ctx context.Context, audience, scope string) (string, error)
}

// HTTPClient calls one deployed agent's routes: /health, /hello, /catalog,
// /call and /captures. The audience of every token is the target id.
type HTTPClient struct {
	Target string
	URL    string
	Client *http.Client
	Tokens TokenSource
}

// Health is an agent's unauthenticated readiness and version report.
type Health struct {
	Status       string `json:"status"`
	APIVersion   int    `json:"apiVersion"`
	Required     int    `json:"required,omitempty"`
	ConfigDigest string `json:"configDigest,omitempty"`
	Error        string `json:"error,omitempty"`
}

// AgentHello is what an agent says about itself over HTTP.
type AgentHello struct {
	APIVersion int                  `json:"apiVersion"`
	Protocol   int                  `json:"protocol"`
	Started    time.Time            `json:"started"`
	Runtime    protocol.Runtime     `json:"runtime"`
	Probes     []protocol.ProbeSpec `json:"probes"`
	Ops        []protocol.OpSpec    `json:"ops"`
}

// Capture is an armed capture as the agent describes it.
type Capture struct {
	ID          string `json:"id"`
	Streaming   bool   `json:"streaming"`
	ContentType string `json:"contentType"`
	FileName    string `json:"fileName"`
}

// Dump is one reading of a capture.
type Dump struct {
	Data     []byte
	Complete bool
}

func (c HTTPClient) validate() error {
	switch {
	case c.Target == "":
		return errors.New("direct agent: target id is required")
	case c.URL == "":
		return fmt.Errorf("direct agent %s: endpoint URL is required", c.Target)
	case c.Client == nil:
		return fmt.Errorf("direct agent %s: HTTP client is required", c.Target)
	case c.Tokens == nil:
		return fmt.Errorf("direct agent %s: token source is required", c.Target)
	}
	return nil
}

// Health reads /health. A 503 outdated agent is a Health, not an error.
func (c HTTPClient) Health(ctx context.Context) (Health, error) {
	response, err := c.send(ctx, http.MethodGet, "/health", "", nil)
	if err != nil {
		return Health{}, err
	}
	var health Health
	if err := decodeHealth(response, &health); err != nil {
		return Health{}, fmt.Errorf("agent %s health: %w", c.Target, err)
	}
	return health, nil
}

func decodeHealth(response *http.Response, health *Health) error {
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusServiceUnavailable {
		return statusError(response)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(health); err != nil {
		return fmt.Errorf("decode HTTP %d: %w", response.StatusCode, err)
	}
	if health.Status == "" {
		return fmt.Errorf("HTTP %d without a status", response.StatusCode)
	}
	return nil
}

// Hello reads /hello.
func (c HTTPClient) Hello(ctx context.Context) (AgentHello, error) {
	var hello AgentHello
	return hello, c.json(ctx, http.MethodGet, "/hello", ScopeRead, nil, &hello)
}

// Catalog reads /catalog.
func (c HTTPClient) Catalog(ctx context.Context) ([]protocol.OpSpec, error) {
	var ops []protocol.OpSpec
	return ops, c.json(ctx, http.MethodGet, "/catalog", ScopeRead, nil, &ops)
}

// Call runs one operation under scope.
func (c HTTPClient) Call(ctx context.Context, scope string, request CallRequest) (json.RawMessage, error) {
	params := request.Params
	if params == nil {
		params = json.RawMessage(`{}`)
	}
	body := map[string]any{"operation": request.Op, "params": params, "allowMutation": request.AllowMutation}
	var value json.RawMessage
	if err := c.json(ctx, http.MethodPost, "/call", scope, body, &value); err != nil {
		return nil, fmt.Errorf("agent call %s: %w", request.Op, err)
	}
	return value, nil
}

// Open arms a capture of kind under scope.
func (c HTTPClient) Open(ctx context.Context, scope, kind string, params json.RawMessage, allowMutation bool) (Capture, error) {
	var capture Capture
	body := map[string]any{"kind": kind, "params": params, "allowMutation": allowMutation}
	if err := c.json(ctx, http.MethodPost, "/captures", scope, body, &capture); err != nil {
		return Capture{}, fmt.Errorf("arm %s: %w", kind, err)
	}
	if capture.ID == "" {
		return Capture{}, fmt.Errorf("arm %s: agent %s returned no capture id", kind, c.Target)
	}
	return capture, nil
}

// Dump reads the capture so far.
func (c HTTPClient) Dump(ctx context.Context, scope, id string) (Dump, error) {
	return c.artifact(ctx, http.MethodPost, "/captures/"+id, scope)
}

// Renew extends the capture's JVM-owned lease.
func (c HTTPClient) Renew(ctx context.Context, scope, id string) error {
	response, err := c.send(ctx, http.MethodPost, "/captures/"+id+"/renew", scope, nil)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("renew capture %s: %w", id, statusError(response))
	}
	return nil
}

// Finish ends the capture and returns its last reading.
func (c HTTPClient) Finish(ctx context.Context, scope, id string) (Dump, error) {
	return c.artifact(ctx, http.MethodDelete, "/captures/"+id, scope)
}

func (c HTTPClient) artifact(ctx context.Context, method, path, scope string) (Dump, error) {
	response, err := c.send(ctx, method, path, scope, nil)
	if err != nil {
		return Dump{}, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return Dump{}, fmt.Errorf("%s %s: %w", method, path, statusError(response))
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxArtifact+1))
	if err != nil {
		return Dump{}, fmt.Errorf("%s %s: %w", method, path, err)
	}
	if len(data) > maxArtifact {
		return Dump{}, fmt.Errorf("%s %s: capture exceeds %d bytes", method, path, maxArtifact)
	}
	return Dump{Data: data, Complete: response.Header.Get("X-Telltale-Complete") == "true"}, nil
}

func (c HTTPClient) json(ctx context.Context, method, path, scope string, body, into any) error {
	response, err := c.send(ctx, method, path, scope, body)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return statusError(response)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxArtifact)).Decode(into); err != nil {
		return fmt.Errorf("decode %s %s: %w", method, path, err)
	}
	return nil
}

// send issues one request; an empty scope sends no token (only /health).
func (c HTTPClient) send(ctx context.Context, method, path, scope string, body any) (*http.Response, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.URL, "/")+path, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if scope != "" {
		token, err := c.Tokens.Token(ctx, c.Target, scope)
		if err != nil {
			return nil, fmt.Errorf("token for %s on %s: %w", scope, c.Target, err)
		}
		request.Header.Set(TokenHeader, "Bearer "+token)
	}
	response, err := c.Client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("agent %s: %w", c.Target, err)
	}
	return response, nil
}

func statusError(response *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	var body struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(data, &body) == nil && body.Error != "" {
		return fmt.Errorf("HTTP %d: %s", response.StatusCode, body.Error)
	}
	return fmt.Errorf("HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(data)))
}
