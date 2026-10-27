package cli_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/flanksource/captain/pkg/ai"
	"github.com/flanksource/captain/pkg/ai/middleware"
	clickyexec "github.com/flanksource/clicky/exec"
	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/traces"
	telltale "github.com/flanksource/telltale/sdk"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type cliAgentProvider struct{}

func (cliAgentProvider) GetModel() string       { return "example-model" }
func (cliAgentProvider) GetRuntime() ai.Runtime { return ai.RuntimeOf(ai.Anthropic, ai.ModeAPI) }
func (cliAgentProvider) Execute(context.Context, ai.Request) (*ai.Response, error) {
	return &ai.Response{Text: "response token=response-secret"}, nil
}

func TestCLIAgentChild(t *testing.T) {
	dir := os.Getenv("TELLTALE_CLI_AGENT_TEST_DIR")
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
	provider, err := middleware.Wrap(cliAgentProvider{}, middleware.WithObservers())
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("ready")
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		result := clickyexec.NewExec("echo", "--password", "exec-secret").WithoutShell().Run().Result()
		if result.Error != nil {
			t.Fatal(result.Error)
		}
		var request ai.Request
		request.Prompt.User = "prompt password=prompt-secret"
		if _, err := provider.Execute(context.Background(), request); err != nil {
			t.Fatal(err)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
}

var _ = Describe("CLI agent captures", func() {
	DescribeTable("captures a child agent through the CLI", func(kind string, flags []string, expected, secret string) {
		config := GinkgoT().TempDir()
		child := exec.Command(os.Args[0], "-test.run=^TestCLIAgentChild$")
		child.Env = append(os.Environ(), "TELLTALE_CLI_AGENT_TEST_DIR="+config)
		stdin, err := child.StdinPipe()
		Expect(err).NotTo(HaveOccurred())
		stdout, err := child.StdoutPipe()
		Expect(err).NotTo(HaveOccurred())
		child.Stderr = GinkgoWriter
		Expect(child.Start()).To(Succeed())
		DeferCleanup(func() { Expect(stdin.Close()).To(Succeed()); Expect(child.Wait()).To(Succeed()) })
		scanner := bufio.NewScanner(stdout)
		Expect(scanner.Scan()).To(BeTrue())
		Expect(scanner.Text()).To(Equal("ready"))
		Expect(agentclient.Default.Discover(context.Background(), filepath.Join(config, "telltale", "agents"))).To(Succeed())
		stop := make(chan struct{})
		done := make(chan struct{})
		go func() {
			defer close(done)
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-stop:
					return
				case <-ticker.C:
					if _, err := fmt.Fprintln(stdin, "run"); err != nil {
						return
					}
				}
			}
		}()
		workDir := GinkgoT().TempDir()
		args := append([]string{"run", kind, "--work-dir", workDir, "--pid", strconv.Itoa(child.Process.Pid), "--duration", "2s"}, flags...)
		_, stderr, runErr := execute(args...)
		close(stop)
		<-done
		Expect(runErr).NotTo(HaveOccurred(), stderr)
		result := manifest(workDir)
		Expect(result.Captures).To(HaveLen(1))
		Expect(result.Captures[0].Kind).To(Equal(kind))
		runtime, closeStore, err := traces.Reopen(result.Dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(closeStore()).To(Succeed()) })
		var rows []recordstore.Row
		Expect(runtime.Results.Backend.Scan(context.Background(), result.Captures[0].Ref.Stream, 0, func(_ int64, row recordstore.Row) error { rows = append(rows, row); return nil })).To(Succeed())
		data, err := json.Marshal(rows)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(ContainSubstring(expected))
		Expect(string(data)).NotTo(ContainSubstring(secret))
		if kind == "llm" {
			Expect(string(data)).To(ContainSubstring("prompt password"))
			Expect(string(data)).To(ContainSubstring("response token"))
			Expect(string(data)).NotTo(ContainSubstring("response-secret"))
		}
	},
		Entry("exec", "exec", []string{"--tail-bytes", "128"}, "--password", "exec-secret"),
		Entry("llm", "llm", []string{"--include-prompts", "--include-responses"}, "example-model", "prompt-secret"),
	)
})
