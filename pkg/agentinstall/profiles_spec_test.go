package agentinstall_test

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/agentinstall"
	"github.com/flanksource/telltale/pkg/credentials/apitoken"
	"github.com/flanksource/telltale/pkg/datadir"
	"github.com/flanksource/telltale/pkg/otlp/profiles"
	"github.com/flanksource/telltale/pkg/traces/read"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/httptop"
	"github.com/flanksource/telltale/pkg/tracing/sqltop"
	"github.com/flanksource/telltale/pkg/tracing/tracingtest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type profilesWorkload struct {
	input    io.WriteCloser
	lines    chan string
	pid      int
	javaHome string
}

func startProfilesWorkload() profilesWorkload {
	GinkgoHelper()
	directory := GinkgoT().TempDir()
	javaHome := os.Getenv("TELLTALE_JVM_INTEGRATION_JAVA_HOME")
	output, err := exec.Command(profilesExecutable(javaHome, "javac"), "-d", directory, "testdata/ProfilesWorkload.java").CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), string(output))
	dependency, err := filepath.Abs("../../sdk/java/build/libs/integration-fixtures/h2-2.2.224.jar")
	Expect(err).NotTo(HaveOccurred())
	_, err = os.Stat(dependency)
	Expect(err).NotTo(HaveOccurred(), "build the packaged Java agent and its declared test fixture artifacts before integration tests")
	child := exec.Command(profilesExecutable(javaHome, "java"), "-cp", directory+string(os.PathListSeparator)+dependency, "ProfilesWorkload")
	child.Stderr = GinkgoWriter
	stdout, err := child.StdoutPipe()
	Expect(err).NotTo(HaveOccurred())
	stdin, err := child.StdinPipe()
	Expect(err).NotTo(HaveOccurred())
	Expect(child.Start()).To(Succeed())
	lines := make(chan string, 16)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if strings.HasPrefix(scanner.Text(), "FIXTURE ") {
				lines <- scanner.Text()
			}
		}
	}()
	DeferCleanup(func() {
		_ = stdin.Close()
		_ = child.Process.Kill()
		_ = child.Wait()
	})
	workload := profilesWorkload{input: stdin, lines: lines, pid: child.Process.Pid, javaHome: javaHome}
	workload.await("FIXTURE READY")
	return workload
}

func profilesExecutable(home, executable string) string {
	if home == "" {
		return executable
	}
	return filepath.Join(home, "bin", executable)
}

func (w profilesWorkload) await(message string) {
	GinkgoHelper()
	var line string
	Eventually(w.lines).WithTimeout(15 * time.Second).Should(Receive(&line))
	Expect(line).To(Equal(message))
}

func (w profilesWorkload) command(command, message string) {
	GinkgoHelper()
	_, err := fmt.Fprintln(w.input, command)
	Expect(err).NotTo(HaveOccurred())
	w.await(message)
}

func attachProfilesHub(workload profilesWorkload) *agentclient.Registry {
	GinkgoHelper()
	directory := GinkgoT().TempDir()
	tokens := agentclient.NewTokenStore([]datadir.ConfigDir{{Name: datadir.ConfigGlobal, Dir: directory}})
	token, err := tokens.Mint("profile-jvm", apitoken.ScopeAgent)
	Expect(err).NotTo(HaveOccurred())
	registry := agentclient.NewRegistry()
	server := httptest.NewServer(agentclient.NewHub(registry, tokens))
	DeferCleanup(server.Close)
	artifact, err := (agentinstall.Java{}).Artifact()
	Expect(err).NotTo(HaveOccurred(), "build the packaged Java agent before integration tests")
	jar := filepath.Join(directory, artifact.Name)
	Expect(os.WriteFile(jar, artifact.Data, 0o600)).To(Succeed())
	config, err := json.Marshal(map[string]any{"url": server.URL, "token": token.Secret.Value(), "service": "profile-fixture", "allowMutation": true,
		"resource": map[string]string{"deployment.environment.name": "profiles-integration"}})
	Expect(err).NotTo(HaveOccurred())
	configPath := filepath.Join(directory, "agent.json")
	Expect(os.WriteFile(configPath, config, 0o600)).To(Succeed())
	args := (agentinstall.Java{}).Attach(agentinstall.Attachment{PID: workload.pid, Artifact: jar, Config: configPath, JavaHome: workload.javaHome})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	attached := exec.CommandContext(ctx, args[0], args[1:]...)
	attached.Stderr = GinkgoWriter
	Expect(attached.Run()).To(Succeed())
	Eventually(func() int { return len(registry.Match(nil)) }).WithTimeout(10 * time.Second).Should(Equal(1))
	agent := registry.Match(nil)[0]
	By("receiving an authenticated HELLO from JVM " + agent.Hello.Runtime.Version)
	Expect(agent.Hello.AgentID).To(HavePrefix("hub/"))
	Expect(agent.Hello.Service).To(Equal("profile-fixture"))
	Expect(agent.Hello.Resource).To(HaveKeyWithValue("process.pid", strconv.Itoa(workload.pid)))
	Expect(agent.Hello.Resource).To(HaveKeyWithValue("deployment.environment.name", "profiles-integration"))
	Expect(agent.Hello.Resource["telltale.source.instance"]).NotTo(BeEmpty())
	DeferCleanup(agent.Close)
	return registry
}

func latestProfileRows(ref read.Ref) read.TopResult {
	GinkgoHelper()
	var result read.TopResult
	Eventually(func(g Gomega) {
		var err error
		result, err = read.Top(context.Background(), ref, read.TopQuery{})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(result.Rows).NotTo(BeEmpty())
	}).WithTimeout(15 * time.Second).Should(Succeed())
	return result
}

var _ = Describe("attached JVM Profiles over authenticated hub sessions", func() {
	It("captures warmed HTTP and JDBC operations, updates the same IDs and clears both Top tables", func() {
		workload := startProfilesWorkload()
		registry := attachProfilesHub(workload)
		previous := agentclient.Default
		agentclient.Default = registry
		DeferCleanup(func() { agentclient.Default = previous })
		agent := registry.Match(nil)[0]
		env := tracingtest.NewEnv(map[string]tracing.TracePlugin{"http_top": httptop.NewKind(registry), "sql_top": sqltop.Kind()})
		params, err := json.Marshal(map[string]any{"target": map[string]any{"seam": "agent", "agent": agent.Hello.AgentID}, "poll": "100ms"})
		Expect(err).NotTo(HaveOccurred())
		refs := map[string]read.Ref{}
		for _, kind := range []string{"http_top", "sql_top"} {
			session := env.Start(kind, string(params))
			tracingtest.Running(session)
			info := session.Snapshot()
			Expect(info.Events).NotTo(BeNil())
			refs[kind] = read.Ref{Runtime: env.Runtime, TraceRef: tracing.TraceRef{Kind: kind, Stream: info.Events.Stream}}
		}
		workload.command("START", "FIXTURE ACTIVE")
		for _, kind := range []string{"http_top", "sql_top"} {
			first := latestProfileRows(refs[kind])
			Expect(first.Frame.Source).To(HavePrefix(agent.Hello.Resource["telltale.source.instance"] + "/"))
			identity := "requestId"
			if kind == "sql_top" {
				identity = "operationId"
				Expect(first.Rows).To(ContainElement(And(HaveKeyWithValue("api", "jdbc"), HaveKeyWithValue("sql", ContainSubstring("HOLD()")))))
			} else {
				Expect(first.Rows).To(ContainElement(And(HaveKeyWithValue("direction", "outgoing"), HaveKeyWithValue("method", "GET"), HaveKeyWithValue("url", HaveSuffix("/hold")))))
			}
			id := first.Rows[0][identity]
			elapsed := first.Rows[0]["elapsedMs"]
			Eventually(func(g Gomega) {
				next, err := read.Top(context.Background(), refs[kind], read.TopQuery{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(next.Frame.Tick).To(BeNumerically(">", first.Frame.Tick))
				g.Expect(next.Rows).To(ContainElement(And(HaveKeyWithValue(identity, id), HaveKey("elapsedMs"))))
				g.Expect(next.Rows[0]["elapsedMs"]).To(BeNumerically(">", elapsed))
			}).WithTimeout(10 * time.Second).Should(Succeed())
			native, err := base64.StdEncoding.DecodeString(first.Frame.Summary["otlp"].(string))
			Expect(err).NotTo(HaveOccurred())
			decoded, err := profiles.Decode(native, profiles.Options{ExpectedKind: kind, ExpectedCaptureID: first.Frame.Summary["captureId"].(string), ExpectedSource: agent.Hello.Resource["telltale.source.instance"]})
			Expect(err).NotTo(HaveOccurred())
			Expect(decoded).To(HaveLen(1))
			Expect(decoded[0].Sequence).To(Equal(first.Frame.Tick))
			Expect(decoded[0].ProfileID).To(Equal(first.Frame.ID))
			Expect(decoded[0].Resource).To(SatisfyAll(
				HaveKeyWithValue("service.name", agent.Hello.Service),
				HaveKeyWithValue("process.pid", agent.Hello.Resource["process.pid"]),
				HaveKeyWithValue("telltale.source.instance", agent.Hello.Resource["telltale.source.instance"]),
				HaveKeyWithValue("deployment.environment.name", agent.Hello.Resource["deployment.environment.name"]),
			))
		}
		workload.command("FINISH", "FIXTURE FINISHED")
		for _, ref := range refs {
			Eventually(func(g Gomega) {
				result, err := read.Top(context.Background(), ref, read.TopQuery{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(result.Rows).To(BeEmpty())
				g.Expect(result.Frame.Summary).To(HaveKeyWithValue("complete", true))
				frames, err := read.Frames(context.Background(), ref)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(frames).To(ContainElement(HaveField("Retained", BeNumerically(">", 0))))
			}).WithTimeout(15 * time.Second).Should(Succeed())
		}
	})
})
