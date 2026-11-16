package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/flanksource/clicky/rpc"
	"github.com/golang-jwt/jwt/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/deploy"
)

const (
	deployedIssuer = "connection://example/signing"
	deployedTarget = "deployer/orders/shop/orders-0"
)

// deployedListener is a deployed agent's listener: it admits a request only
// with a token the deployer's issuer signed for this target, and records
// whom each token names.
func deployedListener(key *rsa.PublicKey, subjects *[]string) http.Handler {
	admit := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			raw, _ := strings.CutPrefix(r.Header.Get(agentclient.TokenHeader), "Bearer ")
			claims := jwt.MapClaims{}
			if _, err := jwt.ParseWithClaims(raw, claims, func(*jwt.Token) (any, error) { return key, nil }, jwt.WithValidMethods([]string{"RS256"}),
				jwt.WithAudience(deployedTarget), jwt.WithIssuer(deployedIssuer), jwt.WithExpirationRequired()); err != nil || claims["scope"] != "jvm:read" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			*subjects = append(*subjects, claims["sub"].(string))
			next(w, r)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /hello", admit(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"apiVersion": agentclient.AgentAPIVersion, "protocol": 1, "started": "2026-01-02T03:04:05Z", "runtime": map[string]string{"lang": "java"},
			"ops": []map[string]any{{"name": "jvm.status", "mutates": false, "scope": "jvm:read"}}})
	}))
	mux.HandleFunc("POST /call", admit(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"allowMutation":false,"instrumentation":true,"patches":[]}`)
	}))
	return mux
}

var _ = Describe("jvm commands on deployed targets", func() {
	It("reach a stored deployer's pod by its target id, signing each request with the deployer's issuer", func() {
		dir := GinkgoT().TempDir()
		GinkgoT().Chdir(dir)
		GinkgoT().Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
		DeferCleanup(func(previous *agentclient.Registry) { agentclient.Default = previous }, agentclient.Default)
		agentclient.Default = agentclient.NewRegistry()

		key, err := rsa.GenerateKey(rand.Reader, 2048)
		Expect(err).NotTo(HaveOccurred())
		der, err := x509.MarshalPKCS8PrivateKey(key)
		Expect(err).NotTo(HaveOccurred())
		connection, err := json.Marshal(map[string]any{"apiVersion": "mission-control.flanksource.com/v1", "kind": "Connection", "metadata": map[string]string{"name": "signing", "namespace": "example"},
			"spec": map[string]any{"http": map[string]string{"url": "http://localhost"}, "secrets": map[string]any{"signingKey": map[string]string{"value": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))}}}})
		Expect(err).NotTo(HaveOccurred())
		Expect(os.MkdirAll(filepath.Join(dir, ".connections"), 0o700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, ".connections", "signing.json"), connection, 0o600)).To(Succeed())

		var subjects []string
		listener := httptest.NewServer(deployedListener(&key.PublicKey, &subjects))
		DeferCleanup(listener.Close)
		deployer, err := deploy.Decode([]byte(`
apiVersion: telltale.flanksource.com/v1alpha1
kind: Deployer
metadata: {name: orders}
spec:
  selector: {kind: Deployment, namespace: shop, labels: {app: orders}, container: app}
  javaContainer: {kind: attach, url: 'http://{{.Pod.IP}}:7070', pid: 1, directory: /tmp/telltale, bind: 0.0.0.0, port: 7070, service: orders}
  auth: {issuer: ` + deployedIssuer + `}
`))
		Expect(err).NotTo(HaveOccurred())
		deployer.Status = deploy.Status{Targets: []deploy.TargetStatus{{Target: deployedTarget, Namespace: "shop", Pod: "orders-0", Container: "app", State: deploy.StateDeployed, Endpoint: listener.URL}}}
		store, err := deployerStore()
		Expect(err).NotTo(HaveOccurred())
		Expect(store.Save(*deployer)).To(Succeed())

		root := NewRoot("test")
		var out bytes.Buffer
		root.SetArgs([]string{"jvm", "status", "--agent", deployedTarget})
		root.SetOut(&out)
		root.SetErr(&bytes.Buffer{})
		Expect(root.ExecuteContext(context.Background())).To(Succeed())
		Expect(out.String()).To(Equal("mutations denied · instrumentation on · 0 patches\n"))
		subject, err := operator()
		Expect(err).NotTo(HaveOccurred())
		Expect(subjects).To(Equal([]string{subject, subject}))

		root = NewRoot("test")
		out.Reset()
		root.SetArgs([]string{"jvm", "deployers", "--format", "json"})
		root.SetOut(&out)
		Expect(root.ExecuteContext(context.Background())).To(Succeed())
		var listed []deploy.TargetStatus
		Expect(json.Unmarshal(out.Bytes(), &listed)).To(Succeed(), out.String())
		Expect(listed).To(Equal(deployer.Status.Targets))
	})

	It("refuses a target no deployer deployed, naming the deploy command", func() {
		GinkgoT().Setenv("XDG_CONFIG_HOME", GinkgoT().TempDir())
		DeferCleanup(func(previous *agentclient.Registry) { agentclient.Default = previous }, agentclient.Default)
		agentclient.Default = agentclient.NewRegistry()
		Expect(executeJVM(context.Background(), "metrics", "--agent", "deployer/missing/shop/orders-0")).To(MatchError(ContainSubstring("telltale jvm deploy -f")))
	})

	It("serves deploy over RPC with the deployer document and a dry run as its inputs", func() {
		spec := kindOpenAPI(NewRoot("test"))
		Expect(spec.Paths).To(HaveKey("/api/v1/jvm/deploy"))
		body := spec.Paths["/api/v1/jvm/deploy"]["post"].RequestBody.Content["application/json"].Schema.Properties
		Expect(body).To(And(HaveKeyWithValue("deployer", HaveField("Type", "string")), HaveKeyWithValue("dry-run", HaveField("Type", "boolean"))))
	})

	It("refuses to read a deployer file for a request that arrived over HTTP", func() {
		file := filepath.Join(GinkgoT().TempDir(), "deployer.yaml")
		Expect(os.WriteFile(file, []byte("kind: Deployer"), 0o600)).To(Succeed())
		remote := rpc.ContextWithRequest(context.Background(), httptest.NewRequest(http.MethodPost, "/api/v1/jvm/deploy", nil))
		Expect(executeJVM(remote, "deploy", "--dry-run", "-f", file)).To(MatchError(ContainSubstring("send deployer in the request instead")))
	})

	It("takes exactly one of a deployer file and a deployer document", func() {
		Expect(executeJVM(context.Background(), "deploy", "--dry-run")).To(MatchError(ContainSubstring("[file deployer]")))
		Expect(executeJVM(context.Background(), "deploy", "-f", "x.yaml", "--deployer", "kind: Deployer")).To(MatchError(ContainSubstring("none of the others")))
	})
})
