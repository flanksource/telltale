package deploy_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/golang-jwt/jwt/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/deploy"
	"github.com/flanksource/telltale/pkg/deploy/auth"
)

// verifyingAgent is a deployed agent's /hello and /call that, like the real
// listener, admits only a token its issuer signed for this target and scope.
type verifyingAgent struct {
	key    *rsa.PublicKey
	target string
	mu     sync.Mutex
	grants []string
}

func (v *verifyingAgent) admit(scope string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw, found := strings.CutPrefix(r.Header.Get(agentclient.TokenHeader), "Bearer ")
		claims := jwt.MapClaims{}
		_, err := jwt.ParseWithClaims(raw, claims, func(*jwt.Token) (any, error) { return v.key, nil },
			jwt.WithValidMethods([]string{"RS256"}), jwt.WithAudience(v.target), jwt.WithIssuer(issuerRef), jwt.WithExpirationRequired())
		if !found || err != nil || claims["scope"] != scope {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		v.mu.Lock()
		v.grants = append(v.grants, claims["sub"].(string)+" "+scope)
		v.mu.Unlock()
		next(w, r)
	}
}

func (v *verifyingAgent) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /hello", v.admit("jvm:read", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"apiVersion": agentclient.AgentAPIVersion, "protocol": 1, "started": "2026-01-02T03:04:05Z", "runtime": map[string]string{"lang": "java"},
			"ops": []map[string]any{{"name": "jvm.metrics", "mutates": false, "scope": "jvm:read"}}})
	}))
	mux.HandleFunc("POST /call", v.admit("jvm:read", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"heapUsed":42}`)
	}))
	return mux
}

var _ = Describe("deployer store and target resolver", func() {
	target := deploy.TargetID("example", namespace, "orders-0")

	// deployed is the attach deployer as `jvm deploy` stores it: deployed to
	// one target whose listener the endpoint reaches.
	deployed := func(endpoint string) deploy.Deployer {
		deployer := attachDeployer()
		deployer.Status = deploy.Status{Targets: []deploy.TargetStatus{{Target: target, Namespace: namespace, Pod: "orders-0", Container: jvm, State: deploy.StateDeployed, AgentAPIVersion: agentclient.AgentAPIVersion, Endpoint: endpoint}}}
		return deployer
	}

	It("keeps applied deployers with their status, by name", func() {
		store := deploy.Store{Dir: GinkgoT().TempDir()}
		Expect(store.Save(deployed("http://127.0.0.1:7070"))).To(Succeed())
		loaded, err := store.Load("example")
		Expect(err).ToNot(HaveOccurred())
		Expect(loaded.Status).To(Equal(deployed("http://127.0.0.1:7070").Status))
		Expect(loaded.Spec.JavaContainer.Kind).To(Equal("attach"))
		listed, err := store.List()
		Expect(err).ToNot(HaveOccurred())
		Expect(listed).To(HaveLen(1))
		_, err = store.Load("missing")
		Expect(err).To(MatchError(ContainSubstring(`deployer "missing" has not been deployed`)))
		_, err = store.Load("../escape")
		Expect(err).To(MatchError(ContainSubstring("invalid deployer name")))
	})

	It("lists deployed target metadata for agent inventory and excludes failed targets", func() {
		store := deploy.Store{Dir: GinkgoT().TempDir()}
		value := deployed("http://127.0.0.1:7070")
		value.Status.Targets = append(value.Status.Targets, deploy.TargetStatus{Target: deploy.TargetID("example", namespace, "orders-1"),
			Namespace: namespace, Pod: "orders-1", Container: jvm, State: deploy.StateFailed, Error: "not deployed"})
		Expect(store.Save(value)).To(Succeed())
		known, err := store.Known()
		Expect(err).NotTo(HaveOccurred())
		Expect(known).To(Equal([]agentclient.Known{{ID: target, Runtime: "java", Names: []string{"example", "orders-0", namespace + "/orders-0"},
			State: string(deploy.StateDeployed), Endpoint: "http://127.0.0.1:7070", Resource: map[string]string{deploy.AttributeDeployer: "example",
				deploy.AttributeTarget: target, deploy.AttributeNamespace: namespace, deploy.AttributePod: "orders-0", deploy.AttributeContainer: jvm}}}))
	})

	It("connects a stored target on its first use, with tokens its issuer mints for it", func() {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		Expect(err).ToNot(HaveOccurred())
		agent := &verifyingAgent{key: &key.PublicKey, target: target}
		server := httptest.NewServer(agent.handler())
		DeferCleanup(server.Close)
		store := deploy.Store{Dir: GinkgoT().TempDir()}
		Expect(store.Save(deployed(server.URL))).To(Succeed())
		signer, err := auth.NewIssuer(issuerRef, key)
		Expect(err).ToNot(HaveOccurred())
		resolver := deploy.Resolver{Store: store, Subject: "operator@example", Context: func(context.Context) (dbcontext.Context, error) { return dbcontext.New(), nil },
			Issuer: func(_ dbcontext.Context, spec deploy.AuthSpec) (*auth.Issuer, error) {
				Expect(spec.Issuer).To(Equal(issuerRef))
				return signer, nil
			}}
		registry := agentclient.NewRegistry()
		registry.SetResolver(deploy.TargetPrefix, resolver.Resolve)

		claimed, err := registry.Resolve(context.Background(), target)
		Expect(claimed, err).To(BeTrue())
		connected := registry.ByID(target)
		Expect(connected.Hello.Resource).To(HaveKeyWithValue(deploy.AttributeDeployer, "example"))
		value, err := connected.Call(context.Background(), agentclient.CallRequest{Op: "jvm.metrics", Params: json.RawMessage(`{}`)})
		Expect(err).ToNot(HaveOccurred())
		Expect(value).To(MatchJSON(`{"heapUsed":42}`))
		Expect(agent.grants).To(Equal([]string{"operator@example jvm:read", "operator@example jvm:read"}))

		claimed, err = registry.Resolve(context.Background(), "pid/1234")
		Expect(claimed, err).To(BeFalse())
	})

	DescribeTable("refuses a target it cannot connect, naming what to do",
		func(id, message string) {
			store := deploy.Store{Dir: GinkgoT().TempDir()}
			Expect(store.Save(deployed("http://127.0.0.1:1"))).To(Succeed())
			resolver := deploy.Resolver{Store: store, Subject: "operator@example", Context: func(context.Context) (dbcontext.Context, error) { return dbcontext.New(), nil }}
			Expect(resolver.Resolve(context.Background(), agentclient.NewRegistry(), id)).To(MatchError(ContainSubstring(message)))
		},
		Entry("a malformed id", "deployer/example/orders", "want deployer/<deployer>/<namespace>/<pod>"),
		Entry("a deployer never deployed", deploy.TargetID("other", namespace, "orders-0"), "telltale jvm deploy"),
		Entry("a pod the deployer did not deploy", deploy.TargetID("example", namespace, "orders-9"), "has no deployed target"),
	)
})
