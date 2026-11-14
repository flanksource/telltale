package deploy_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	"github.com/flanksource/telltale/pkg/agentinstall"
	"github.com/flanksource/telltale/pkg/deploy"
)

// listenerStub is an attached agent's /health: absent until the fake
// container attaches an agent, then answering with its configured digest.
type listenerStub struct {
	mu         sync.Mutex
	apiVersion int
	digest     string
}

func (l *listenerStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if r.URL.Path != "/health" || l.digest == "" {
		http.NotFound(w, r)
		return
	}
	status, code := "ok", http.StatusOK
	if l.apiVersion != agentclient.AgentAPIVersion {
		status, code = "outdated", http.StatusServiceUnavailable
	}
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"status": status, "apiVersion": l.apiVersion, "configDigest": l.digest})
}

// container plays the JVM container kubectl exec reaches: it keeps what is
// written, answers the checksum of it, and attaching starts the listener
// with the written agent configuration.
type container struct {
	mu       sync.Mutex
	listener *listenerStub
	files    map[string][]byte
	commands []string
	config   map[string]any
}

func (c *container) Run(_ context.Context, args []string, input io.Reader) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.commands = append(c.commands, args[0])
	switch args[0] {
	case "ps":
		return []byte("Thu Jan  1 00:00:00 2026\n"), nil
	case "uname":
		return []byte("Linux\n"), nil
	case "dd":
		data, err := io.ReadAll(input)
		c.files[strings.TrimPrefix(args[1], "of=")] = data
		return nil, err
	case "mv":
		c.files[args[3]] = c.files[args[2]]
		return nil, nil
	case "sha256sum":
		sum := sha256.Sum256(c.files[args[1]])
		return []byte(hex.EncodeToString(sum[:]) + "  " + args[1] + "\n"), nil
	case "java":
		Expect(json.Unmarshal(c.files[args[len(args)-1]], &c.config)).To(Succeed())
		listener := c.config["http"].(map[string]any)
		c.listener.mu.Lock()
		c.listener.digest = listener["configDigest"].(string)
		c.listener.mu.Unlock()
		return nil, nil
	}
	return nil, nil
}

func listenerPod(name string, stub *listenerStub) *corev1.Pod {
	server := httptest.NewServer(stub)
	DeferCleanup(server.Close)
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	Expect(err).ToNot(HaveOccurred())
	number, err := strconv.Atoi(port)
	Expect(err).ToNot(HaveOccurred())
	return pod(name, map[string]string{"app": "orders"}, corev1.PodRunning, corev1.ContainerPort{Name: "telltale", ContainerPort: int32(number)})
}

func attachDeployer() deploy.Deployer {
	deployer := tomcatDeployer()
	spec, err := deploy.NewJavaContainerSpec(map[string]any{"kind": "attach", "url": `http://127.0.0.1:{{.Port "telltale"}}`, "pid": 1, "directory": "/opt/telltale",
		"bind": "0.0.0.0", "port": 7070, "service": "orders", "allowMutation": true})
	Expect(err).ToNot(HaveOccurred())
	deployer.Spec.JavaContainer = spec
	return deployer
}

var _ = Describe("attach java containers", func() {
	It("attaches the agent with a hub-less listener once and leaves a current one alone", func() {
		listener := &listenerStub{apiVersion: agentclient.AgentAPIVersion}
		jvmContainer := &container{listener: listener, files: map[string][]byte{}}
		signer := issuer()
		options := deploy.Options{Kubernetes: fake.NewClientset(listenerPod("orders-0", listener)), Issuer: signer, Exec: func(deploy.Target) agentinstall.Transport { return jvmContainer }}
		status, err := deploy.Deploy(dbcontext.New(), attachDeployer(), options)
		Expect(err).ToNot(HaveOccurred())
		Expect(status.Targets).To(ConsistOf(And(HaveField("State", deploy.StateDeployed), HaveField("AgentAPIVersion", agentclient.AgentAPIVersion))))
		Expect(jvmContainer.commands).To(ContainElement("java"))
		Expect(jvmContainer.config).ToNot(HaveKey("url"), "a hub-less agent dials no hub")
		Expect(jvmContainer.config["http"]).To(And(HaveKeyWithValue("bind", "0.0.0.0"), HaveKeyWithValue("port", 7070.0), HaveKeyWithValue("issuer", issuerRef),
			HaveKeyWithValue("audience", deploy.TargetID("example", namespace, "orders-0")), HaveKey("jwks")))

		commands := len(jvmContainer.commands)
		again, err := deploy.Deploy(dbcontext.New(), attachDeployer(), options)
		Expect(err).ToNot(HaveOccurred())
		Expect(again).To(Equal(status))
		Expect(jvmContainer.commands).To(HaveLen(commands), "a current agent is not attached again")
	})

	It("reports outdated, without attaching, when the JVM runs another agent API", func() {
		listener := &listenerStub{apiVersion: agentclient.AgentAPIVersion + 1, digest: "any"}
		jvmContainer := &container{listener: listener, files: map[string][]byte{}}
		status, err := deploy.Deploy(dbcontext.New(), attachDeployer(), deploy.Options{Kubernetes: fake.NewClientset(listenerPod("orders-0", listener)), Issuer: issuer(), Exec: func(deploy.Target) agentinstall.Transport { return jvmContainer }})
		Expect(err).To(MatchError(ContainSubstring("outdated")))
		Expect(status.Targets).To(ConsistOf(HaveField("State", deploy.StateOutdated)))
		Expect(jvmContainer.commands).To(BeEmpty())
	})
})
