package agentinstall_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/agentinstall"
	"github.com/flanksource/telltale/pkg/deploy/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type profilesHTTPAgent struct {
	registry   *agentclient.Registry
	agent      *agentclient.Agent
	attachment agentinstall.Attachment
	client     agentclient.HTTPClient
	portFile   string
}

func attachProfilesHTTP(workload profilesWorkload) *agentclient.Registry {
	return connectProfilesHTTP(workload).registry
}

func connectProfilesHTTP(workload profilesWorkload) profilesHTTPAgent {
	GinkgoHelper()
	directory := GinkgoT().TempDir()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	Expect(err).NotTo(HaveOccurred())
	issuer, err := auth.NewIssuer("profiles-integration", key)
	Expect(err).NotTo(HaveOccurred())
	const target = "profiles-http-fixture"
	portFile := filepath.Join(directory, "port")
	config, err := json.Marshal(map[string]any{"token": "profiles-local-fixture-token", "service": "profile-fixture", "allowMutation": true,
		"resource": map[string]string{"deployment.environment.name": "profiles-integration"},
		"http":     map[string]any{"bind": "127.0.0.1", "port": 0, "portFile": portFile, "issuer": issuer.Name(), "audience": target, "jwks": issuer.JWKS()}})
	Expect(err).NotTo(HaveOccurred())
	configPath := filepath.Join(directory, "agent.json")
	Expect(os.WriteFile(configPath, config, 0o600)).To(Succeed())
	artifact, err := (agentinstall.Java{}).Artifact()
	Expect(err).NotTo(HaveOccurred())
	jar := filepath.Join(directory, artifact.Name)
	Expect(os.WriteFile(jar, artifact.Data, 0o600)).To(Succeed())
	attachment := agentinstall.Attachment{PID: workload.pid, Artifact: jar, Config: configPath, JavaHome: workload.javaHome}
	args := (agentinstall.Java{}).Attach(attachment)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	attached := exec.CommandContext(ctx, args[0], args[1:]...)
	attached.Stderr = GinkgoWriter
	Expect(attached.Run()).To(Succeed())
	port, err := os.ReadFile(portFile)
	Expect(err).NotTo(HaveOccurred())
	agent, err := agentclient.DialHTTP(ctx, agentclient.HTTPEndpoint{ID: target, URL: "http://127.0.0.1:" + string(port), Client: http.DefaultClient,
		Tokens: auth.Tokens{Issuer: issuer, Subject: "profiles-fixture", TTL: time.Minute}, Resource: map[string]string{"telltale.deployer": "profiles-fixture"}, Poll: 100 * time.Millisecond})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(agent.Close)
	Expect(agent.Hello.Resource).To(SatisfyAll(
		HaveKeyWithValue("telltale.deployer", "profiles-fixture"), HaveKeyWithValue("service.name", "profile-fixture"),
		HaveKeyWithValue("process.pid", strconv.Itoa(workload.pid)), HaveKeyWithValue("deployment.environment.name", "profiles-integration"),
	))
	Expect(agent.Hello.Resource["telltale.source.instance"]).NotTo(BeEmpty())
	registry := agentclient.NewRegistry()
	Expect(registry.Add(agent)).To(Succeed())
	return profilesHTTPAgent{registry: registry, agent: agent, attachment: attachment, portFile: portFile,
		client: agentclient.HTTPClient{Target: target, URL: "http://127.0.0.1:" + string(port), Client: http.DefaultClient, Tokens: auth.Tokens{Issuer: issuer, Subject: "profiles-fixture", TTL: time.Minute}}}
}
