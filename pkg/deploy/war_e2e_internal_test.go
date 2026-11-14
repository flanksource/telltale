package deploy

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	dbcontext "github.com/flanksource/commons-db/context"
	dutyKubernetes "github.com/flanksource/commons-db/kubernetes"
	"github.com/golang-jwt/jwt/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	"github.com/flanksource/telltale/pkg/agentclient"
	javaagent "github.com/flanksource/telltale/pkg/agentinstall/java"
	"github.com/flanksource/telltale/pkg/deploy/auth"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/jvmtrace"
	"github.com/flanksource/telltale/pkg/tracing/tracingtest"
)

// harnessJar is the embedded Tomcat that `gradlew tomcatHarnessJar` builds.
var harnessJar = filepath.Join("..", "..", "sdk", "java", "build", "libs", "telltale-tomcat-harness.jar")

const harnessWorkload = "com.flanksource.telltale.harness.HarnessWorkload"

func signingKey() *rsa.PrivateKey {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	Expect(err).ToNot(HaveOccurred())
	return key
}

// startHarness runs telltale-diag.war, provisioned for target, in a real
// Tomcat JVM and returns its execution URL.
func startHarness(target Target, provision Provision) string {
	_, err := os.Stat(harnessJar)
	Expect(err).ToNot(HaveOccurred(), "build the harness: ./sdk/java/gradlew -p sdk/java war tomcatHarnessJar")
	directory := GinkgoT().TempDir()
	container := &tomcat{spec: TomcatSpec{ContextPath: DefaultContextPath, RuntimeDir: filepath.Join(directory, "runtime"), AllowMutation: true}}
	config, err := adapterConfig(target, provision, container.adapterSettings())
	Expect(err).ToNot(HaveOccurred())
	war, err := javaagent.War()
	Expect(err).ToNot(HaveOccurred(), "build the embedded WAR: ./sdk/java/gradlew -p sdk/java war")
	war, err = configureWAR(war, config)
	Expect(err).ToNot(HaveOccurred())
	path := filepath.Join(directory, "telltale-diag.war")
	Expect(os.WriteFile(path, war, 0o600)).To(Succeed())
	harness := exec.Command("java", "-jar", harnessJar, path, DefaultContextPath)
	stdin, err := harness.StdinPipe()
	Expect(err).ToNot(HaveOccurred())
	stdout, err := harness.StdoutPipe()
	Expect(err).ToNot(HaveOccurred())
	harness.Stderr = GinkgoWriter
	Expect(harness.Start()).To(Succeed())
	DeferCleanup(func() { _ = stdin.Close(); _ = harness.Wait() })
	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if port, found := strings.CutPrefix(scanner.Text(), "READY "); found {
				ready <- port
			}
		}
	}()
	var port string
	Eventually(ready, time.Minute).Should(Receive(&port))
	endpoint := "http://127.0.0.1:" + port + DefaultContextPath
	found, health, err := inspect(context.Background(), listenerClient(), target, endpoint, config["configDigest"])
	Expect(err).ToNot(HaveOccurred())
	Expect(found).To(Equal(current), "the WAR echoes the provisioned digest: %+v", health)
	return endpoint
}

func harnessTarget() Target {
	return Target{ID: TargetID("e2e", "local", "harness"), Pod: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "harness", Namespace: "local"}}, Container: corev1.Container{Name: "tomcat"}}
}

const apiServerIdentity = "apiserver-identity"

// apiServerProxy plays the apiserver's pods/proxy: it authenticates its own
// identity, then deletes Authorization (as k8s.io/apiserver's authentication
// filter does) and forwards everything else to the pod.
type apiServerProxy struct {
	base string
	mu   sync.Mutex
	// forwarded counts requests that authenticated as the apiserver identity
	// and carried an execution token in X-Telltale-Authorization.
	forwarded int
}

func (a *apiServerProxy) forwardedTokens() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.forwarded
}

func fakeAPIServer(backend string) *apiServerProxy {
	target, err := url.Parse(backend)
	Expect(err).ToNot(HaveOccurred())
	const prefix = "/api/v1/namespaces/local/pods/harness:8080/proxy"
	proxy := &apiServerProxy{}
	forward := httputil.NewSingleHostReverseProxy(target)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+apiServerIdentity {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"kind":"Status","code":401,"message":"Unauthorized"}`)
			return
		}
		if !strings.HasPrefix(r.URL.Path, prefix) {
			http.NotFound(w, r)
			return
		}
		proxy.mu.Lock()
		if r.Header.Get(agentclient.TokenHeader) != "" {
			proxy.forwarded++
		}
		proxy.mu.Unlock()
		r.Header.Del("Authorization")
		r.URL.Path = strings.TrimPrefix(r.URL.Path, prefix)
		forward.ServeHTTP(w, r)
	}))
	DeferCleanup(server.Close)
	proxy.base = server.URL + prefix
	Expect(dutyKubernetes.RegisterAPIServerProxy(proxy.base, &rest.Config{Host: server.URL, BearerToken: apiServerIdentity})).To(Succeed())
	return proxy
}

// catalogStatus is the HTTP status of GET /catalog with bearer token.
func catalogStatus(endpoint, token string) int {
	request, err := http.NewRequest(http.MethodGet, endpoint+"/catalog", nil)
	Expect(err).ToNot(HaveOccurred())
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := http.DefaultClient.Do(request)
	Expect(err).ToNot(HaveOccurred())
	Expect(response.Body.Close()).To(Succeed())
	return response.StatusCode
}

// forged signs claims with key under kid, as no honest issuer would.
func forged(key *rsa.PrivateKey, kid string, claims jwt.MapClaims) string {
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = kid
	signed, err := token.SignedString(key)
	Expect(err).ToNot(HaveOccurred())
	return signed
}

var _ = Describe("telltale-diag.war over direct HTTP", Label("jvm"), func() {
	It("verifies every execution token and picks up a rotated key from the issuer's JWKS", func() {
		key := signingKey()
		issuer, err := auth.NewIssuer("e2e-issuer", key)
		Expect(err).ToNot(HaveOccurred())
		jwks := httptest.NewServer(issuer)
		DeferCleanup(jwks.Close)
		target := harnessTarget()
		endpoint := startHarness(target, Provision{Issuer: issuer.Name(), JWKS: issuer.JWKS(), JWKSURL: jwks.URL})
		kid := issuer.JWKS().Keys[0].KeyID
		valid := jwt.MapClaims{"iss": issuer.Name(), "aud": target.ID, "sub": "e2e", "scope": "jvm:read", "exp": time.Now().Add(time.Minute).Unix()}
		with := func(key string, value any) jwt.MapClaims {
			claims := jwt.MapClaims{}
			for name, v := range valid {
				claims[name] = v
			}
			claims[key] = value
			return claims
		}
		Expect(catalogStatus(endpoint, "")).To(Equal(http.StatusUnauthorized), "a missing token")
		Expect(catalogStatus(endpoint, forged(signingKey(), kid, valid))).To(Equal(http.StatusUnauthorized), "a bad signature")
		Expect(catalogStatus(endpoint, forged(key, kid, with("aud", TargetID("e2e", "local", "other"))))).To(Equal(http.StatusUnauthorized), "another target's token")
		Expect(catalogStatus(endpoint, forged(key, kid, with("exp", time.Now().Add(-5*time.Minute).Unix())))).To(Equal(http.StatusUnauthorized), "an expired token")
		Expect(catalogStatus(endpoint, forged(key, kid, with("scope", "jvm:trace")))).To(Equal(http.StatusForbidden), "an insufficient scope")
		Expect(catalogStatus(endpoint, forged(key, kid, valid))).To(Equal(http.StatusOK), "a correctly scoped token")

		Expect(issuer.Rotate(signingKey())).To(Succeed())
		rotated, err := issuer.Mint(auth.Claims{Audience: target.ID, Subject: "e2e", Scopes: []auth.Scope{auth.ScopeRead}}, time.Minute)
		Expect(err).ToNot(HaveOccurred())
		Expect(catalogStatus(endpoint, rotated)).To(Equal(http.StatusOK), "the WAR refreshes its JWKS on the new kid")
	})

	It("executes through an apiserver proxy that deletes Authorization, with the token in X-Telltale-Authorization", func() {
		issuer, err := auth.NewIssuer("e2e-issuer", signingKey())
		Expect(err).ToNot(HaveOccurred())
		target := harnessTarget()
		endpoint := startHarness(target, Provision{Issuer: issuer.Name(), JWKS: issuer.JWKS()})
		apiserver := fakeAPIServer(strings.TrimSuffix(endpoint, DefaultContextPath))
		proxied := apiserver.base + DefaultContextPath
		client, err := (&tomcat{spec: TomcatSpec{ContextPath: DefaultContextPath}}).Client(dbcontext.New(), "local")
		Expect(err).ToNot(HaveOccurred())
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		agent, err := agentclient.DialHTTP(ctx, agentclient.HTTPEndpoint{ID: target.ID, URL: proxied, Client: client, Tokens: auth.Tokens{Issuer: issuer, Subject: "e2e", TTL: time.Minute}})
		Expect(err).ToNot(HaveOccurred(), "the client's transport carries the apiserver identity, the token rides X-Telltale-Authorization")
		DeferCleanup(agent.Close)
		value, err := agent.Call(ctx, agentclient.CallRequest{Op: "jvm.jmx.search", Params: json.RawMessage(`{"pattern":"java.lang:type=Runtime"}`)})
		Expect(err).ToNot(HaveOccurred())
		Expect(value).To(MatchJSON(`["java.lang:type=Runtime"]`))
		Expect(apiserver.forwardedTokens()).To(BeNumerically(">=", 2), "hello and the call reached the WAR through the proxy with the token in X-Telltale-Authorization")

		token, err := issuer.Mint(auth.Claims{Audience: target.ID, Subject: "e2e", Scopes: []auth.Scope{auth.ScopeRead}}, time.Minute)
		Expect(err).ToNot(HaveOccurred())
		request, err := http.NewRequest(http.MethodGet, proxied+"/catalog", nil)
		Expect(err).ToNot(HaveOccurred())
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := client.Do(request)
		Expect(err).ToNot(HaveOccurred())
		Expect(response.Body.Close()).To(Succeed())
		Expect(response.StatusCode).To(Equal(http.StatusUnauthorized), "a bearer in Authorization alone is the apiserver's to judge and never reaches the WAR")
		Expect(catalogStatus(endpoint, token)).To(Equal(http.StatusOK), "a direct connection may still use Authorization")
	})

	It("runs a jvm_trace capture end to end against a deployed target, with no hub", func() {
		issuer, err := auth.NewIssuer("e2e-issuer", signingKey())
		Expect(err).ToNot(HaveOccurred())
		target := harnessTarget()
		endpoint := startHarness(target, Provision{Issuer: issuer.Name(), JWKS: issuer.JWKS()})
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		agent, err := agentclient.DialHTTP(ctx, agentclient.HTTPEndpoint{ID: target.ID, URL: endpoint, Client: listenerClient(), Tokens: auth.Tokens{Issuer: issuer, Subject: "e2e", TTL: time.Minute},
			Resource: map[string]string{AttributeDeployer: "e2e", AttributePod: "harness"}})
		Expect(err).ToNot(HaveOccurred())
		registry := agentclient.NewRegistry()
		Expect(registry.Add(agent)).To(Succeed())
		DeferCleanup(agent.Close)

		env := tracingtest.NewEnv(map[string]tracing.TracePlugin{jvmtrace.Kind: jvmtrace.NewKind(registry)})
		Expect(registry.Match(map[string]string{AttributeDeployer: "e2e", AttributePod: "harness"})).To(ConsistOf(agent), "a capture selects the deployed target by deployer and pod")
		// A selector capture is a fleet capture that waits for late joiners for
		// its whole duration; naming the one target ends with its capture.
		params, err := json.Marshal(map[string]any{"target": map[string]any{"seam": "agent", "agent": target.ID, "allowMutation": true},
			"targets": []map[string]string{{"class": harnessWorkload, "method": "compute"}}, "args": true, "return": true, "events": 4})
		Expect(err).ToNot(HaveOccurred())
		managed, err := env.Runtime.Start(dbcontext.New().Wrap(ctx), tracing.StartRequest{Kind: jvmtrace.Kind, Params: params})
		Expect(err).ToNot(HaveOccurred())
		rows := env.Rows(tracingtest.Ended(managed.Session()))
		Expect(rows).ToNot(BeEmpty())
		Expect(rows).To(ContainElement(And(HaveKeyWithValue("class", harnessWorkload), HaveKeyWithValue("method", "compute"))))
		patches, err := agent.Call(ctx, agentclient.CallRequest{Op: "jvm.classes.patches", Params: json.RawMessage(`{}`)})
		Expect(err).ToNot(HaveOccurred())
		Expect(patches).To(MatchJSON(`[]`), "the capture restored the traced class")
	})
})
