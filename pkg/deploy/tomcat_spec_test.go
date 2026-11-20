package deploy_test

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"

	dbcontext "github.com/flanksource/commons-db/context"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/deploy"
	"github.com/flanksource/telltale/pkg/deploy/auth"
)

const (
	issuerRef       = "connection://example/signing"
	managerUser     = "manager"
	managerPassword = "example"
)

// tomcatStub is the Tomcat manager text API plus the deployed WAR's /health:
// it records each deploy and answers health with the uploaded config digest
// and its JVM's agent API.
type tomcatStub struct {
	mu         sync.Mutex
	apiVersion int
	deploys    int
	config     map[string]string
}

func (t *tomcatStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch {
	case r.Method == http.MethodPut && r.URL.Path == "/manager/text/deploy":
		t.deploy(w, r)
	case r.Method == http.MethodGet && r.URL.Path == deploy.DefaultContextPath+"/health":
		if t.config == nil {
			http.NotFound(w, r)
			return
		}
		status, code := "ok", http.StatusOK
		if t.apiVersion != agentclient.AgentAPIVersion {
			status, code = "outdated", http.StatusServiceUnavailable
		}
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": status, "apiVersion": t.apiVersion, "configDigest": t.config["configDigest"]})
	default:
		http.NotFound(w, r)
	}
}

func (t *tomcatStub) deploy(w http.ResponseWriter, r *http.Request) {
	user, password, ok := r.BasicAuth()
	if !ok || user != managerUser || password != managerPassword {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	Expect(r.URL.Query().Get("path")).To(Equal(deploy.DefaultContextPath))
	Expect(r.URL.Query().Get("update")).To(Equal("true"))
	war, err := io.ReadAll(r.Body)
	Expect(err).ToNot(HaveOccurred())
	t.config = warConfig(war)
	t.deploys++
	_, _ = io.WriteString(w, "OK - Deployed application at context path ["+deploy.DefaultContextPath+"]\n")
}

// warConfig is the properties the deployer embedded in the uploaded WAR.
func warConfig(war []byte) map[string]string {
	reader, err := zip.NewReader(bytes.NewReader(war), int64(len(war)))
	Expect(err).ToNot(HaveOccurred())
	file, err := reader.Open("WEB-INF/telltale-diag.properties")
	Expect(err).ToNot(HaveOccurred())
	data, err := io.ReadAll(file)
	Expect(err).ToNot(HaveOccurred())
	unescape := strings.NewReplacer(`\:`, ":", `\=`, "=", `\#`, "#", `\!`, "!", `\\`, `\`)
	config := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		key, value, found := strings.Cut(line, "=")
		Expect(found).To(BeTrue(), line)
		config[key] = unescape.Replace(value)
	}
	return config
}

func stubPod(name string, stub *tomcatStub) *corev1.Pod {
	server := httptest.NewServer(stub)
	DeferCleanup(server.Close)
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	Expect(err).ToNot(HaveOccurred())
	number, err := strconv.Atoi(port)
	Expect(err).ToNot(HaveOccurred())
	return pod(name, map[string]string{"app": "orders"}, corev1.PodRunning, corev1.ContainerPort{Name: "http", ContainerPort: int32(number)})
}

func tomcatDeployer() deploy.Deployer {
	deployer, err := deploy.Decode([]byte(fmt.Sprintf(`
apiVersion: telltale.flanksource.com/v1alpha1
kind: Deployer
metadata: {name: example}
spec:
  selector: {kind: Pod, namespace: %s, labels: {app: orders}, container: %s}
  javaContainer:
    kind: tomcat
    url: 'http://127.0.0.1:{{.Port "http"}}'
    username: {value: %s}
    password: {value: %s}
    runtimeDir: /opt/telltale/runtime
  auth: {issuer: %s}
`, namespace, jvm, managerUser, managerPassword, issuerRef)))
	Expect(err).ToNot(HaveOccurred())
	return *deployer
}

func issuer() *auth.Issuer {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	Expect(err).ToNot(HaveOccurred())
	signer, err := auth.NewIssuer(issuerRef, key)
	Expect(err).ToNot(HaveOccurred())
	return signer
}

var _ = Describe("tomcat java containers", func() {
	It("deploys a WAR provisioned for each target once, and leaves a current one alone", func() {
		first, second := &tomcatStub{apiVersion: agentclient.AgentAPIVersion}, &tomcatStub{apiVersion: agentclient.AgentAPIVersion}
		options := deploy.Options{Kubernetes: fake.NewClientset(stubPod("orders-0", first), stubPod("orders-1", second)), Issuer: issuer()}
		status, err := deploy.Deploy(dbcontext.New(), tomcatDeployer(), options)
		Expect(err).ToNot(HaveOccurred())
		Expect(status.Targets).To(HaveLen(2))
		for _, target := range status.Targets {
			Expect(target).To(And(HaveField("State", deploy.StateDeployed), HaveField("AgentAPIVersion", agentclient.AgentAPIVersion), HaveField("Endpoint", HaveSuffix(deploy.DefaultContextPath))))
		}
		jwks, err := json.Marshal(options.Issuer.JWKS())
		Expect(err).ToNot(HaveOccurred())
		Expect(first.config).To(And(HaveKeyWithValue("audience", deploy.TargetID("example", namespace, "orders-0")), HaveKeyWithValue("issuer", issuerRef),
			HaveKeyWithValue("jwks", string(jwks)), HaveKeyWithValue("runtimeDir", "/opt/telltale/runtime"), HaveKeyWithValue("allowMutation", "false")))
		Expect(second.config).To(HaveKeyWithValue("audience", deploy.TargetID("example", namespace, "orders-1")))
		Expect(first.config).ToNot(HaveKey(ContainSubstring("token")), "no static token is provisioned")

		again, err := deploy.Deploy(dbcontext.New(), tomcatDeployer(), options)
		Expect(err).ToNot(HaveOccurred())
		Expect(again).To(Equal(status))
		Expect([]int{first.deploys, second.deploys}).To(Equal([]int{1, 1}), "a current target is not redeployed")
	})

	It("deploys over url and records the executionUrl, with the context path ahead of its query, as the endpoint", func() {
		stub := &tomcatStub{apiVersion: agentclient.AgentAPIVersion}
		deployer := tomcatDeployer()
		spec, err := deploy.NewJavaContainerSpec(map[string]any{"kind": "tomcat", "url": `http://127.0.0.1:{{.Port "http"}}`, "executionUrl": `proxy://{{.Pod.Name}}.{{.Pod.Namespace}}:{{.Port "http"}}?kind=pod`,
			"username": map[string]string{"value": managerUser}, "password": map[string]string{"value": managerPassword}, "runtimeDir": "/opt/telltale/runtime"})
		Expect(err).ToNot(HaveOccurred())
		deployer.Spec.JavaContainer = spec
		running := stubPod("orders-0", stub)
		status, err := deploy.Deploy(dbcontext.New(), deployer, deploy.Options{Kubernetes: fake.NewClientset(running), Issuer: issuer()})
		Expect(err).ToNot(HaveOccurred())
		port := running.Spec.Containers[1].Ports[0].ContainerPort
		Expect(status.Targets).To(ConsistOf(And(HaveField("State", deploy.StateDeployed), HaveField("Endpoint", fmt.Sprintf("proxy://orders-0.orders:%d%s?kind=pod", port, deploy.DefaultContextPath)))))
		Expect(stub.deploys).To(Equal(1))
	})

	It("redeploys a target provisioned with another key", func() {
		stub := &tomcatStub{apiVersion: agentclient.AgentAPIVersion}
		kubernetes := fake.NewClientset(stubPod("orders-0", stub))
		_, err := deploy.Deploy(dbcontext.New(), tomcatDeployer(), deploy.Options{Kubernetes: kubernetes, Issuer: issuer()})
		Expect(err).ToNot(HaveOccurred())
		_, err = deploy.Deploy(dbcontext.New(), tomcatDeployer(), deploy.Options{Kubernetes: kubernetes, Issuer: issuer()})
		Expect(err).ToNot(HaveOccurred())
		Expect(stub.deploys).To(Equal(2))
	})

	It("reports outdated when the JVM keeps another agent API after a redeploy", func() {
		stub := &tomcatStub{apiVersion: agentclient.AgentAPIVersion - 1}
		status, err := deploy.Deploy(dbcontext.New(), tomcatDeployer(), deploy.Options{Kubernetes: fake.NewClientset(stubPod("orders-0", stub)), Issuer: issuer()})
		Expect(err).To(MatchError(ContainSubstring("outdated")))
		Expect(status.Targets).To(ConsistOf(And(HaveField("State", deploy.StateOutdated), HaveField("AgentAPIVersion", agentclient.AgentAPIVersion-1), HaveField("Error", ContainSubstring("restart the JVM")))))
	})

	It("fails a target whose manager refuses the login", func() {
		deployer := tomcatDeployer()
		spec, err := deploy.NewJavaContainerSpec(map[string]any{"kind": "tomcat", "url": `http://127.0.0.1:{{.Port "http"}}`, "username": map[string]string{"value": managerUser}, "password": map[string]string{"value": "wrong"}, "runtimeDir": "/opt/telltale/runtime"})
		Expect(err).ToNot(HaveOccurred())
		deployer.Spec.JavaContainer = spec
		status, err := deploy.Deploy(dbcontext.New(), deployer, deploy.Options{Kubernetes: fake.NewClientset(stubPod("orders-0", &tomcatStub{apiVersion: agentclient.AgentAPIVersion})), Issuer: issuer()})
		Expect(err).To(HaveOccurred())
		Expect(status.Targets).To(ConsistOf(And(HaveField("State", deploy.StateFailed), HaveField("Error", ContainSubstring("HTTP 401")))))
	})

	DescribeTable("refuses a javaContainer it cannot run",
		func(spec map[string]any, message string) {
			deployer := tomcatDeployer()
			container, err := deploy.NewJavaContainerSpec(spec)
			Expect(err).ToNot(HaveOccurred())
			deployer.Spec.JavaContainer = container
			_, err = deploy.Deploy(dbcontext.New(), deployer, deploy.Options{Kubernetes: fake.NewClientset(), Issuer: issuer()})
			Expect(err).To(MatchError(ContainSubstring(message)))
		},
		Entry("an unregistered kind", map[string]any{"kind": "jetty"}, `javaContainer kind "jetty" is not registered (registered: attach, javaagent, tomcat)`),
		Entry("javaagent, not implemented yet", map[string]any{"kind": "javaagent"}, "not implemented"),
		Entry("a tomcat without a manager login", map[string]any{"kind": "tomcat", "url": "http://example", "runtimeDir": "/opt/telltale"}, "manager login is required"),
		Entry("a tomcat field it does not know", map[string]any{"kind": "tomcat", "url": "http://example", "runtimeDir": "/opt/telltale", "token": "x"}, `unknown field "token"`),
		Entry("a manager url through the apiserver proxy, which strips Authorization",
			map[string]any{"kind": "tomcat", "url": `proxy://{{.Pod.Name}}.{{.Pod.Namespace}}:8080?kind=pod`, "username": map[string]string{"value": managerUser}, "runtimeDir": "/opt/telltale"},
			"deletes the Authorization header"),
		Entry("a literal apiserver proxy manager url, refused naming portforward",
			map[string]any{"kind": "tomcat", "url": "https://apiserver.example/api/v1/namespaces/orders/pods/orders-0:8080/proxy", "username": map[string]string{"value": managerUser}, "runtimeDir": "/opt/telltale"},
			"deploy over portforward://"),
	)
})
