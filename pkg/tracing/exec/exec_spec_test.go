package exec_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/flanksource/captain/pkg/ai"
	"github.com/flanksource/captain/pkg/ai/middleware"
	clickyexec "github.com/flanksource/clicky/exec"
	_ "github.com/flanksource/commons-db/query/providers/sqlite"
	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/tracing"
	traceexec "github.com/flanksource/telltale/pkg/tracing/exec"
	"github.com/flanksource/telltale/pkg/tracing/llm"
	"github.com/flanksource/telltale/pkg/tracing/tracingtest"
	telltale "github.com/flanksource/telltale/sdk"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCaptures(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Exec and LLM captures") }

func TestAgentChild(t *testing.T) {
	dir := os.Getenv("TELLTALE_TEST_AGENT_DIR")
	if dir == "" {
		return
	}
	agent, err := telltale.Start(telltale.Options{ConfigDir: dir, Listen: "tcp"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := agent.Close(); err != nil {
			t.Error(err)
		}
	}()
	observed, err := middleware.Wrap(provider{}, middleware.WithObservers())
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("ready")
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		result := clickyexec.NewExec("echo", "child-output").WithoutShell().WithExactEnv(map[string]string{"EXAMPLE_KEY": "private-env-value"}).Run().Result()
		if result.Error != nil {
			t.Fatal(result.Error)
		}
		var request ai.Request
		request.Prompt.User = "child prompt"
		if _, err := observed.Execute(context.Background(), request); err != nil {
			t.Fatal(err)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
}

type provider struct{}

func (provider) GetModel() string       { return "example-model" }
func (provider) GetRuntime() ai.Runtime { return ai.RuntimeOf(ai.Anthropic, ai.ModeAPI) }
func (provider) Execute(context.Context, ai.Request) (*ai.Response, error) {
	return &ai.Response{Text: "example response"}, nil
}

var _ = Describe("self captures", func() {
	It("stores observed executions with environment keys and no environment values", func() {
		env := tracingtest.NewEnv(map[string]tracing.TracePlugin{traceexec.Kind: traceexec.NewKind(nil)})
		session := env.Start(traceexec.Kind, `{"name":"echo","tailBytes":64}`)
		tracingtest.Running(session)
		result := clickyexec.NewExec("echo", "observed").WithoutShell().WithExactEnv(map[string]string{"EXAMPLE_KEY": "private-env-value"}).Run().Result()
		Expect(result.Error).NotTo(HaveOccurred())
		Eventually(func() []string {
			rows := env.Rows(session.Snapshot())
			var values []string
			for _, row := range rows {
				values = append(values, fmt.Sprint(row))
			}
			return values
		}).WithTimeout(5 * time.Second).Should(ContainElement(And(ContainSubstring("EXAMPLE_KEY"), ContainSubstring("observed"), Not(ContainSubstring("private-env-value")))))
	})
	It("includes prompts and responses only when explicitly enabled", func() {
		env := tracingtest.NewEnv(map[string]tracing.TracePlugin{llm.Kind: llm.NewKind(nil)})
		without := env.Start(llm.Kind, `{}`)
		with := env.Start(llm.Kind, `{"includePrompts":true,"includeResponses":true}`)
		tracingtest.Running(without)
		tracingtest.Running(with)
		observed, err := middleware.Wrap(provider{}, middleware.WithObservers())
		Expect(err).NotTo(HaveOccurred())
		var request ai.Request
		request.Prompt.User = "example prompt"
		_, err = observed.Execute(context.Background(), request)
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() string { data, _ := json.Marshal(env.Rows(without.Snapshot())); return string(data) }).WithTimeout(5 * time.Second).Should(And(ContainSubstring("example-model"), Not(ContainSubstring("example prompt")), Not(ContainSubstring("example response"))))
		Eventually(func() string { data, _ := json.Marshal(env.Rows(with.Snapshot())); return string(data) }).WithTimeout(5 * time.Second).Should(And(ContainSubstring("example prompt"), ContainSubstring("example response")))
	})
})

var _ = Describe("agent captures", func() {
	It("stores a child agent's exec and opt-in LLM records", func() {
		dir := GinkgoT().TempDir()
		command := osexec.Command(os.Args[0], "-test.run=^TestAgentChild$")
		command.Env = append(os.Environ(), "TELLTALE_TEST_AGENT_DIR="+dir)
		stdin, err := command.StdinPipe()
		Expect(err).NotTo(HaveOccurred())
		stdout, err := command.StdoutPipe()
		Expect(err).NotTo(HaveOccurred())
		command.Stderr = GinkgoWriter
		Expect(command.Start()).To(Succeed())
		DeferCleanup(func() { _ = stdin.Close(); Expect(command.Wait()).To(Succeed()) })
		scanner := bufio.NewScanner(stdout)
		Expect(scanner.Scan()).To(BeTrue())
		Expect(scanner.Text()).To(Equal("ready"))
		registry := agentclient.NewRegistry()
		registry.Dir = filepath.Join(dir, "telltale", "agents")
		env := tracingtest.NewEnv(map[string]tracing.TracePlugin{traceexec.Kind: traceexec.NewKind(registry), llm.Kind: llm.NewKind(registry)})
		selection := fmt.Sprintf(`{"target":{"pid":%d},"tailBytes":64}`, command.Process.Pid)
		executions := env.Start(traceexec.Kind, selection)
		calls := env.Start(llm.Kind, fmt.Sprintf(`{"target":{"pid":%d},"includePrompts":true,"includeResponses":true}`, command.Process.Pid))
		tracingtest.Running(executions)
		tracingtest.Running(calls)
		_, err = fmt.Fprintln(stdin, "run")
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() string { data, _ := json.Marshal(env.Rows(executions.Snapshot())); return string(data) }).WithTimeout(5 * time.Second).Should(And(ContainSubstring("child-output"), ContainSubstring("EXAMPLE_KEY"), Not(ContainSubstring("private-env-value"))))
		Eventually(func() string { data, _ := json.Marshal(env.Rows(calls.Snapshot())); return string(data) }).WithTimeout(5 * time.Second).Should(And(ContainSubstring("child prompt"), ContainSubstring("example response")))
	})
})
