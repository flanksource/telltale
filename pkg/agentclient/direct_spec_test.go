package agentclient_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/sdk/protocol"
)

const directTarget = "deployer:example/orders/orders-0"

// grant is one token the fake agent was asked for.
type grant struct{ audience, scope string }

// recordingTokens mints "<audience>|<scope>" so the fake agent can check what
// each request was granted.
type recordingTokens struct {
	mu     sync.Mutex
	grants []grant
}

func (r *recordingTokens) Token(_ context.Context, audience, scope string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.grants = append(r.grants, grant{audience, scope})
	return audience + "|" + scope, nil
}

// fakeAgent is the direct-HTTP agent contract: a hello naming each op's and
// probe's scope, a call, and a streaming capture that completes on dump
// completeAfter (never when zero). Every authenticated route checks the
// bearer carries its scope.
type fakeAgent struct {
	apiVersion    int
	completeAfter int
	mu            sync.Mutex
	dumps         int
	renewed       int
	finished      bool
}

func (f *fakeAgent) handler() http.Handler {
	mux := http.NewServeMux()
	scoped := func(scope string, next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get(agentclient.TokenHeader) != "Bearer "+directTarget+"|"+scope || r.Header.Get("Authorization") != "" {
				w.WriteHeader(http.StatusForbidden)
				_, _ = io.WriteString(w, `{"error":"want `+scope+`"}`)
				return
			}
			next(w, r)
		}
	}
	mux.HandleFunc("GET /hello", scoped("jvm:read", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"apiVersion": f.apiVersion, "protocol": 1, "started": "2026-01-02T03:04:05Z", "runtime": map[string]string{"lang": "java"},
			"probes": []map[string]any{{"name": "jvm_trace", "checkpoints": true, "mutates": true, "reverts": true, "scope": "jvm:trace"}},
			"ops":    []map[string]any{{"name": "jvm.groovy.eval", "mutates": true, "scope": "jvm:eval"}}})
	}))
	mux.HandleFunc("GET /catalog", scoped("jvm:read", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `[{"name":"jvm.groovy.eval","mutates":true,"scope":"jvm:eval"}]`)
	}))
	mux.HandleFunc("POST /call", scoped("jvm:eval", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		Expect(json.NewDecoder(r.Body).Decode(&body)).To(Succeed())
		Expect(body).To(HaveKeyWithValue("operation", "jvm.groovy.eval"))
		_, _ = io.WriteString(w, `{"value":7}`)
	}))
	mux.HandleFunc("POST /captures", scoped("jvm:trace", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"id":"c1","streaming":true,"contentType":"application/json","fileName":"trace.json"}`)
	}))
	mux.HandleFunc("POST /captures/c1", scoped("jvm:trace", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		f.dumps++
		complete := f.completeAfter > 0 && f.dumps >= f.completeAfter
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Telltale-Complete", map[bool]string{true: "true", false: "false"}[complete])
		_, _ = io.WriteString(w, `{"events":[]}`)
	}))
	mux.HandleFunc("POST /captures/c1/renew", scoped("jvm:trace", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		f.renewed++
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.HandleFunc("DELETE /captures/c1", scoped("jvm:trace", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		f.finished = true
		f.mu.Unlock()
		_, _ = io.WriteString(w, `{"events":["final"]}`)
	}))
	return mux
}

func dialFake(fake *fakeAgent, tokens agentclient.TokenSource) (*agentclient.Agent, error) {
	server := httptest.NewServer(fake.handler())
	DeferCleanup(server.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return agentclient.DialHTTP(ctx, agentclient.HTTPEndpoint{ID: directTarget, URL: server.URL, Client: server.Client(), Tokens: tokens, Resource: map[string]string{"telltale.deployer": "orders"}, Poll: 10 * time.Millisecond})
}

var _ = Describe("Direct HTTP agents", func() {
	It("identifies a deployed agent by its target and asks each call for the scope the agent names", func() {
		tokens := &recordingTokens{}
		agent, err := dialFake(&fakeAgent{apiVersion: agentclient.AgentAPIVersion}, tokens)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(agent.Close)
		Expect(agent.Hello.AgentID).To(Equal(directTarget))
		Expect(agent.Hello.Resource).To(HaveKeyWithValue("telltale.deployer", "orders"))
		Expect(agent.HasProbe("jvm_trace")).To(BeTrue())
		value, err := agent.Call(context.Background(), agentclient.CallRequest{Op: "jvm.groovy.eval", Params: json.RawMessage(`{"source":"3+4"}`), AllowMutation: true})
		Expect(err).ToNot(HaveOccurred())
		Expect(value).To(MatchJSON(`{"value":7}`))
		_, err = agent.Call(context.Background(), agentclient.CallRequest{Op: "jvm.unknown"})
		Expect(err).To(MatchError(ContainSubstring(`does not advertise operation "jvm.unknown"`)))
		Expect(tokens.grants).To(ContainElements(grant{directTarget, "jvm:read"}, grant{directTarget, "jvm:eval"}))
	})

	It("refuses an agent speaking another agent API", func() {
		_, err := dialFake(&fakeAgent{apiVersion: agentclient.AgentAPIVersion + 1}, &recordingTokens{})
		Expect(err).To(MatchError(ContainSubstring("agent API")))
	})

	It("streams a capture's dumps as blobs and ends it complete when the agent says so", func() {
		fake := &fakeAgent{apiVersion: agentclient.AgentAPIVersion, completeAfter: 3}
		agent, err := dialFake(fake, &recordingTokens{})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(agent.Close)
		session, err := agent.Arm(context.Background(), agentclient.ArmRequest{Probe: "jvm_trace", Params: json.RawMessage(`{}`), Mutates: true, AllowMutation: true})
		Expect(err).ToNot(HaveOccurred())
		var blob agentclient.Blob
		Eventually(session.Blobs, 2*time.Second).Should(Receive(&blob))
		Expect(blob.Info).To(And(HaveField("Name", "trace.json"), HaveField("ContentType", "application/json"), HaveField("Session", session.ID)))
		var ended protocol.Ended
		Eventually(session.Ended, 2*time.Second).Should(Receive(&ended))
		Expect(ended).To(And(HaveField("Complete", true), HaveField("Error", "")))
		Expect(session.Disarm(context.Background())).To(Succeed())
		fake.mu.Lock()
		defer fake.mu.Unlock()
		Expect(fake.finished).To(BeTrue(), "a completed capture is finished on the agent")
	})

	It("answers a checkpoint with a dump and returns the final dump on disarm", func() {
		fake := &fakeAgent{apiVersion: agentclient.AgentAPIVersion}
		agent, err := dialFake(fake, &recordingTokens{})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(agent.Close)
		session, err := agent.Arm(context.Background(), agentclient.ArmRequest{Probe: "jvm_trace", Params: json.RawMessage(`{}`), Mutates: true, AllowMutation: true})
		Expect(err).ToNot(HaveOccurred())
		checkpoint, err := session.Checkpoint(context.Background(), json.RawMessage(`{"reason":"step"}`))
		Expect(err).ToNot(HaveOccurred())
		Expect(checkpoint.Blobs).To(HaveLen(1))
		Expect(checkpoint.Blobs[0].CheckpointParams).To(MatchJSON(`{"reason":"step"}`))
		Expect(session.Disarm(context.Background())).To(Succeed())
		var final agentclient.Blob
		Eventually(session.Blobs).Should(Receive(&final))
		for strings.Contains(string(final.Data), `"events":[]`) {
			Eventually(session.Blobs).Should(Receive(&final))
		}
		Expect(final.Data).To(MatchJSON(`{"events":["final"]}`))
	})
})
