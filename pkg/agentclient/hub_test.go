package agentclient_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/credentials/apitoken"
	"github.com/flanksource/telltale/sdk"
	"github.com/flanksource/telltale/sdk/protocol"
	"github.com/gorilla/websocket"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type revertProbe struct{ active atomic.Bool }

func (*revertProbe) Spec() protocol.ProbeSpec {
	return protocol.ProbeSpec{Name: "hub-revert", Mutates: true, Reverts: true}
}
func (p *revertProbe) Arm(context.Context, json.RawMessage, telltale.Sink) (func() error, error) {
	p.active.Store(true)
	return func() error { p.active.Store(false); return nil }, nil
}

var _ = Describe("agent hub", func() {
	It("refuses a remote agent that claims a numeric local identity", func() {
		store := agentclient.NewTokenStore(GinkgoT().TempDir())
		token, err := store.Mint("spoof", apitoken.ScopeAgent)
		Expect(err).NotTo(HaveOccurred())
		registry := agentclient.NewRegistry()
		server := httptest.NewServer(agentclient.NewHub(registry, store))
		DeferCleanup(server.Close)
		u, err := url.Parse(server.URL)
		Expect(err).NotTo(HaveOccurred())
		u.Scheme = "ws"
		u.Path = "/api/agents/connect"
		conn, _, err := websocket.DefaultDialer.Dial(u.String(), http.Header{"Authorization": []string{"Bearer " + token.Secret.Value()}})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = conn.Close() })
		stream := &telltale.WebSocketStream{Conn: conn}
		frames := protocol.NewConn(stream)
		welcome, _, err := frames.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(welcome.Type).To(Equal("welcome"))
		Expect(frames.Send("auth", protocol.Welcome{Accepted: true})).To(Succeed())
		Expect(frames.Send("hello", protocol.Hello{AgentID: strconv.Itoa(os.Getpid()), Protocol: protocol.ProtocolVersion, Resource: map[string]string{"process.pid": strconv.Itoa(os.Getpid())}})).To(Succeed())
		Expect(stream.SetReadDeadline(time.Now().Add(5 * time.Second))).To(Succeed())
		_, _, err = frames.Read()
		Expect(err).To(HaveOccurred())
		Expect(registry.Match(nil)).To(BeEmpty())
		Expect(registry.ByPID(os.Getpid())).To(BeNil())
	})
	It("rejects an otherwise valid token with the wrong scope", func() {
		dir := GinkgoT().TempDir()
		store := agentclient.NewTokenStore(dir)
		existing, err := store.Mint("agent", apitoken.ScopeAgent)
		Expect(err).NotTo(HaveOccurred())
		data, err := json.Marshal(apitoken.Record{ID: existing.ID, SecretHash: existing.Hash, Name: "agent", Scope: apitoken.ScopeGit})
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(dir, ".telltale", "agent-tokens", existing.ID+".json"), data, 0600)).To(Succeed())
		req := httptest.NewRequest(http.MethodGet, "/api/agents/connect", nil)
		req.Header.Set("Authorization", "Bearer "+existing.Secret.Value())
		response := httptest.NewRecorder()
		agentclient.NewHub(agentclient.NewRegistry(), store).ServeHTTP(response, req)
		Expect(response.Code).To(Equal(http.StatusForbidden))
		Expect(response.Body.String()).To(MatchJSON(`{"error":"forbidden"}`))
	})
	It("distinguishes missing and wrong credentials from corrupt token storage", func() {
		dir := GinkgoT().TempDir()
		store := agentclient.NewTokenStore(dir)
		token, err := store.Mint("test", "agent")
		Expect(err).NotTo(HaveOccurred())
		hub := agentclient.NewHub(agentclient.NewRegistry(), store)
		for _, value := range []string{"", "Bearer " + strings.Replace(token.Secret.Value(), ".", ".wrong", 1)} {
			req := httptest.NewRequest(http.MethodGet, "/api/agents/connect", nil)
			if value != "" {
				req.Header.Set("Authorization", value)
			}
			w := httptest.NewRecorder()
			hub.ServeHTTP(w, req)
			Expect(w.Code).To(Equal(http.StatusUnauthorized))
			Expect(w.Body.String()).To(MatchJSON(`{"error":"unauthorized"}`))
		}
		for _, data := range []string{"{", `{"ID":"` + token.ID + `","Scope":"agent","SecretHash":"broken"}`} {
			Expect(os.WriteFile(filepath.Join(dir, ".telltale", "agent-tokens", token.ID+".json"), []byte(data), 0600)).To(Succeed())
			req := httptest.NewRequest(http.MethodGet, "/api/agents/connect", nil)
			req.Header.Set("Authorization", "Bearer "+token.Secret.Value())
			w := httptest.NewRecorder()
			hub.ServeHTTP(w, req)
			Expect(w.Code).To(Equal(http.StatusInternalServerError))
			Expect(w.Body.String()).To(MatchJSON(`{"error":"token verification failed"}`))
		}
		path := filepath.Join(dir, ".telltale", "agent-tokens", token.ID+".json")
		Expect(os.Remove(path)).To(Succeed())
		Expect(os.Mkdir(path, 0700)).To(Succeed())
		req := httptest.NewRequest(http.MethodGet, "/api/agents/connect", nil)
		req.Header.Set("Authorization", "Bearer "+token.Secret.Value())
		w := httptest.NewRecorder()
		hub.ServeHTTP(w, req)
		Expect(w.Code).To(Equal(http.StatusInternalServerError))
	})
	It("rejects unauthenticated requests and registers a dial-out agent", func() {
		store := agentclient.NewTokenStore(GinkgoT().TempDir())
		token, err := store.Mint("test-agent", "agent")
		Expect(err).NotTo(HaveOccurred())
		registry := agentclient.NewRegistry()
		server := httptest.NewServer(agentclient.NewHub(registry, store))
		DeferCleanup(server.Close)
		request, err := http.NewRequest(http.MethodGet, server.URL+"/api/agents/connect", nil)
		Expect(err).NotTo(HaveOccurred())
		request.Header.Set("Authorization", "Bearer invalid")
		response, err := http.DefaultClient.Do(request)
		Expect(err).NotTo(HaveOccurred())
		Expect(response.Body.Close()).To(Succeed())
		Expect(response.StatusCode).To(Equal(http.StatusUnauthorized))
		agent, err := telltale.Start(telltale.Options{Dial: server.URL, Token: token.Secret.Value(), ConfigDir: GinkgoT().TempDir()})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(agent.Close()).To(Succeed()) })
		Eventually(func() int { return len(registry.Match(nil)) }, 5*time.Second, 50*time.Millisecond).Should(Equal(1))
		Expect(registry.ByPID(os.Getpid())).To(BeNil())
		Expect(registry.Match(map[string]string{"process.pid": strconv.Itoa(os.Getpid())})).To(HaveLen(1))
		Expect(registry.Match(nil)[0].Hello.AgentID).To(HavePrefix("hub/"))
	})
	It("matches multiple agents, arms a late joiner and reconnects after a disconnect", func() {
		store := agentclient.NewTokenStore(GinkgoT().TempDir())
		token, err := store.Mint("fleet", "agent")
		Expect(err).NotTo(HaveOccurred())
		registry := agentclient.NewRegistry()
		server := httptest.NewServer(agentclient.NewHub(registry, store))
		DeferCleanup(server.Close)
		start := func(pod string) *telltale.Agent {
			agent, err := telltale.Start(telltale.Options{Dial: server.URL, Token: token.Secret.Value(), ConfigDir: GinkgoT().TempDir(), Labels: map[string]string{"app": "sample", "k8s.pod.name": pod}})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { Expect(agent.Close()).To(Succeed()) })
			return agent
		}
		start("one")
		start("two")
		selector := map[string]string{"app": "sample"}
		Eventually(func() int { return len(registry.Match(selector)) }, 5*time.Second).Should(Equal(2))
		joined, unsubscribe := registry.Subscribe()
		DeferCleanup(unsubscribe)
		start("three")
		var late *agentclient.Agent
		Eventually(joined, 5*time.Second).Should(Receive(&late))
		Expect(late.Hello.Resource).To(HaveKeyWithValue("k8s.pod.name", "three"))
		session, err := late.Arm(context.Background(), agentclient.ArmRequest{Probe: "stacks", Params: json.RawMessage(`{}`)})
		Expect(err).NotTo(HaveOccurred())
		result, err := session.Checkpoint(context.Background(), json.RawMessage(`{}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Blobs).To(HaveLen(1))
		Expect(session.Disarm(context.Background())).To(Succeed())
		id := late.Hello.AgentID
		Expect(late.Close()).To(Succeed())
		Eventually(func() bool { current := registry.ByID(id); return current != nil && current != late }, 10*time.Second).Should(BeTrue())
	})
	It("reverts a mutating probe when the hub connection closes", func() {
		probe := &revertProbe{}
		telltale.RegisterProbe(probe)
		store := agentclient.NewTokenStore(GinkgoT().TempDir())
		token, err := store.Mint("mutating-agent", "agent")
		Expect(err).NotTo(HaveOccurred())
		registry := agentclient.NewRegistry()
		server := httptest.NewServer(agentclient.NewHub(registry, store))
		DeferCleanup(server.Close)
		agent, err := telltale.Start(telltale.Options{Dial: server.URL, Token: token.Secret.Value(), ConfigDir: GinkgoT().TempDir(), AllowMutation: true})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(agent.Close()).To(Succeed()) })
		Eventually(func() int { return len(registry.Match(nil)) }, 5*time.Second).Should(Equal(1))
		remote := registry.Match(nil)[0]
		_, err = remote.Arm(context.Background(), agentclient.ArmRequest{Probe: "hub-revert", Params: json.RawMessage(`{}`), Mutates: true, AllowMutation: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(probe.active.Load()).To(BeTrue())
		Expect(remote.Close()).To(Succeed())
		Eventually(probe.active.Load, 5*time.Second).Should(BeFalse())
	})
})
