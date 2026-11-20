package agentinstall_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"

	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/agentinstall"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func versionedAgent(original, version string) string {
	GinkgoHelper()
	data, err := os.ReadFile(original)
	Expect(err).NotTo(HaveOccurred())
	input, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	Expect(err).NotTo(HaveOccurred())
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	for _, file := range input.File {
		reader, err := file.Open()
		Expect(err).NotTo(HaveOccurred())
		content, err := io.ReadAll(reader)
		Expect(err).NotTo(HaveOccurred())
		Expect(reader.Close()).To(Succeed())
		if file.Name == "META-INF/MANIFEST.MF" {
			pattern := regexp.MustCompile(`(?m)^Implementation-Version:[^\n]*`)
			Expect(pattern.Match(content)).To(BeTrue())
			content = pattern.ReplaceAll(content, []byte("Implementation-Version: "+version+"\r"))
		}
		writer, err := archive.CreateHeader(&file.FileHeader)
		Expect(err).NotTo(HaveOccurred())
		_, err = writer.Write(content)
		Expect(err).NotTo(HaveOccurred())
	}
	Expect(archive.Close()).To(Succeed())
	path := filepath.Join(GinkgoT().TempDir(), "telltale-agent.jar")
	Expect(os.WriteFile(path, output.Bytes(), 0o600)).To(Succeed())
	return path
}

func attachUpgrade(attachment agentinstall.Attachment) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args := (agentinstall.Java{}).Attach(attachment)
	return exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()
}

var _ = Describe("live attached agent upgrades", func() {
	It("replaces repeated runtime versions in one JVM and preserves the old runtime when a capture is active", func() {
		workload := startProfilesWorkload()
		connected := connectProfilesHTTP(workload)
		source := connected.agent.Hello.Resource["telltale.source.instance"]
		attachment := connected.attachment
		attachment.Artifact = versionedAgent(attachment.Artifact, "upgrade-fixture-two")
		output, err := attachUpgrade(attachment)
		Expect(err).To(HaveOccurred(), string(output))
		Expect(string(output)).To(ContainSubstring("--upgrade"))
		attachment.Upgrade = true
		session, err := connected.agent.Arm(context.Background(), agentclient.ArmRequest{Probe: "http_top", Mutates: true, AllowMutation: true})
		Expect(err).NotTo(HaveOccurred())
		output, err = attachUpgrade(attachment)
		Expect(err).To(HaveOccurred(), string(output))
		Expect(string(output)).To(ContainSubstring("active captures"))
		Expect(session.Disarm(context.Background())).To(Succeed())
		for _, version := range []string{"upgrade-fixture-two", "upgrade-fixture-three"} {
			attachment.Artifact = versionedAgent(connected.attachment.Artifact, version)
			output, err = attachUpgrade(attachment)
			Expect(err).NotTo(HaveOccurred(), string(output))
			port, err := os.ReadFile(connected.portFile)
			Expect(err).NotTo(HaveOccurred())
			connected.client.URL = "http://127.0.0.1:" + string(port)
			health, err := connected.client.Health(context.Background())
			Expect(err).NotTo(HaveOccurred())
			Expect(health).To(And(HaveField("Status", "ok"), HaveField("RuntimeVersion", version), HaveField("UpgradeAPIVersion", 1)))
			hello, err := connected.client.Hello(context.Background())
			Expect(err).NotTo(HaveOccurred())
			Expect(hello.SDKVersion).To(Equal(version))
			Expect(hello.Resource).To(And(HaveKeyWithValue("process.pid", connected.agent.Hello.Resource["process.pid"]), HaveKeyWithValue("telltale.source.instance", source)))
			_, err = connected.client.Call(context.Background(), "jvm:read", agentclient.CallRequest{Op: "jvm.metrics"})
			Expect(err).NotTo(HaveOccurred())
		}
		occupied, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(occupied.Close)
		config, err := os.ReadFile(attachment.Config)
		Expect(err).NotTo(HaveOccurred())
		var values map[string]any
		Expect(json.Unmarshal(config, &values)).To(Succeed())
		values["http"].(map[string]any)["port"] = occupied.Addr().(*net.TCPAddr).Port
		invalid, err := json.Marshal(values)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(attachment.Config, invalid, 0o600)).To(Succeed())
		attachment.Artifact = versionedAgent(connected.attachment.Artifact, "upgrade-fixture-failed")
		output, err = attachUpgrade(attachment)
		Expect(err).To(HaveOccurred(), string(output))
		Expect(string(output)).To(ContainSubstring("previous runtime was restored"))
		port, err := os.ReadFile(connected.portFile)
		Expect(err).NotTo(HaveOccurred())
		connected.client.URL = "http://127.0.0.1:" + string(port)
		health, err := connected.client.Health(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(health.RuntimeVersion).To(Equal("upgrade-fixture-three"))
	})
})

func attachUpgradedProfilesHTTP(workload profilesWorkload) *agentclient.Registry {
	GinkgoHelper()
	connected := connectProfilesHTTP(workload)
	attachment := connected.attachment
	attachment.Upgrade = true
	attachment.Artifact = versionedAgent(attachment.Artifact, "profiles-upgrade-fixture")
	output, err := attachUpgrade(attachment)
	Expect(err).NotTo(HaveOccurred(), string(output))
	port, err := os.ReadFile(connected.portFile)
	Expect(err).NotTo(HaveOccurred())
	client := connected.client
	agent, err := agentclient.DialHTTP(context.Background(), agentclient.HTTPEndpoint{ID: client.Target, URL: "http://127.0.0.1:" + string(port), Client: client.Client, Tokens: client.Tokens, Resource: map[string]string{"telltale.deployer": "profiles-fixture"}})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(agent.Close)
	registry := agentclient.NewRegistry()
	Expect(registry.Add(agent)).To(Succeed())
	return registry
}
