package mcpproxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// received is what the upstream saw of the one request it was sent.
type received struct {
	path, host, body string
	header           http.Header
}

func startUpstream(t *testing.T) (*httptest.Server, chan received) {
	t.Helper()
	got := make(chan received, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got <- received{path: r.URL.Path, host: r.Host, body: string(body), header: r.Header.Clone()}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	t.Cleanup(upstream.Close)
	return upstream, got
}

// The proxy forwards to the upstream's host under the path the client used,
// with the body intact, the injected header set and the client's own headers
// passed through, and reports the JSON-RPC call it relayed.
func TestForwardsToolsCallWithInjectedHeaders(t *testing.T) {
	upstream, got := startUpstream(t)
	proxy, err := Start("acme", upstream.URL+"/mcp", map[string]string{"X-Acme-Tenant": "tenant-x"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(proxy.Close)
	var observed []ObservationEvent
	proxy.SetObserver(func(event ObservationEvent) { observed = append(observed, event) })

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"acme__search","arguments":{}}}`
	req, err := http.NewRequest(http.MethodPost, proxy.URL(), strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer client-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	forwarded := <-got
	upstreamHost := strings.TrimPrefix(upstream.URL, "http://")
	if forwarded.path != "/mcp" || forwarded.host != upstreamHost || forwarded.body != body {
		t.Errorf("forwarded path=%q host=%q body=%q, want /mcp %q and the client's body", forwarded.path, forwarded.host, forwarded.body, upstreamHost)
	}
	if v := forwarded.header.Get("X-Acme-Tenant"); v != "tenant-x" {
		t.Errorf("injected header = %q, want tenant-x", v)
	}
	if v := forwarded.header.Get("Authorization"); v != "Bearer client-token" {
		t.Errorf("client Authorization = %q, want it passed through", v)
	}
	if len(observed) != 1 || observed[0].RPCMethod != "tools/call" || observed[0].Tool != "acme__search" || observed[0].Status != http.StatusOK {
		t.Errorf("observed = %+v, want one tools/call of acme__search answered 200", observed)
	}
}

// Rewrite (unlike the deprecated Director) drops forwarding headers the client
// sent and adds none of its own, so a client cannot spoof where a request
// came from and the upstream learns nothing of the proxy's own address.
func TestForwardsNoForwardingHeaders(t *testing.T) {
	upstream, got := startUpstream(t)
	proxy, err := Start("acme", upstream.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(proxy.Close)

	req, err := http.NewRequest(http.MethodGet, proxy.URL()+"/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Forwarded-For", "203.0.113.7")
	req.Header.Set("Forwarded", "for=203.0.113.7")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}

	forwarded := <-got
	for _, name := range []string{"X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "Forwarded"} {
		if v := forwarded.header.Values(name); len(v) > 0 {
			t.Errorf("%s = %v, want none forwarded", name, v)
		}
	}
}

func TestStartRefusesAnUpstreamWithoutSchemeOrHost(t *testing.T) {
	for _, upstream := range []string{"localhost:8080", "/mcp", "http://"} {
		if _, err := Start("acme", upstream, nil); err == nil {
			t.Errorf("Start(%q) succeeded, want an error naming the missing scheme or host", upstream)
		}
	}
	if _, err := Start("acme", "http://[::1", nil); err == nil {
		t.Error("Start with a malformed URL succeeded, want a parse error")
	}
}
