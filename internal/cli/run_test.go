package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/internal/cli"
	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/target"
	"github.com/flanksource/telltale/pkg/tracing/terminal"
	"github.com/flanksource/telltale/pkg/tracing/tracingtest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// execute runs the CLI with args, returning what it wrote and how it ended.
func execute(args ...string) (string, string, error) {
	root := cli.NewRoot("dev")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetArgs(args)
	err := root.Execute()
	return stdout.String(), stderr.String(), err
}

func help(args ...string) string {
	stdout, stderr, err := execute(append(args, "--help")...)
	Expect(err).ToNot(HaveOccurred(), stderr)
	return stdout
}

// manifest is the one run workDir recorded.
func manifest(workDir string) telltale.Result {
	runs, err := telltale.List(telltale.ListOptions{WorkDir: workDir})
	Expect(err).ToNot(HaveOccurred())
	Expect(runs).To(HaveLen(1))
	data, err := os.ReadFile(filepath.Join(runs[0].Dir, telltale.ManifestFile))
	Expect(err).ToNot(HaveOccurred())
	var result telltale.Result
	Expect(json.Unmarshal(data, &result)).To(Succeed())
	return result
}

var _ = Describe("telltale run <kind>", func() {
	It("has a command for every registered kind, listed in run's help", func() {
		page := help("run")
		for _, registration := range traces.Registrations() {
			Expect(page).To(ContainSubstring(registration.Name), "run --help lists %s", registration.Name)
			Expect(page).To(ContainSubstring(registration.Summary))
		}
		Expect(page).To(ContainSubstring("telltale ps"))
		Expect(page).To(ContainSubstring("telltale gops"))
	})

	It("generates a command for a kind registered before the CLI is built", func() {
		Expect(traces.Register(traces.Registration{
			Name: "cli_test_kind", Title: "CLI test kind", Summary: "Records what the CLI test feeds it", Seams: []target.Seam{target.Self}, DefaultSeam: target.Self,
			Description: "Registered by the CLI test.",
			Examples:    []traces.Example{{Args: "--width 80 -- true", Says: "an 80-cell terminal"}},
			New:         func(traces.KindDeps) tracing.TracePlugin { return terminal.NewKind() },
		})).To(Succeed())
		page := help("run", "cli_test_kind")
		Expect(page).To(ContainSubstring("Registered by the CLI test."))
		Expect(page).To(ContainSubstring("telltale run cli_test_kind --width 80 -- true"))
		Expect(page).To(ContainSubstring("--width int"))
	})

	It("shows each kind's description, target, params with their defaults and choices, and examples", func() {
		for _, registration := range traces.Registrations() {
			page := help("run", registration.Name)
			Expect(page).To(ContainSubstring(registration.Description), registration.Name)
			Expect(page).To(ContainSubstring("TRACE PARAMS"), registration.Name)
			for _, example := range registration.Examples {
				Expect(page).To(ContainSubstring("telltale run " + registration.Name + " " + example.Args))
			}
		}
		page := help("run", "http")
		Expect(page).To(MatchRegexp(`(?s)COMMAND FLAGS.*TRACE PARAMS.*FORMATTING`), "a kind's params come before the formatting flags")
		Expect(page).To(ContainSubstring("--mode string"))
		Expect(page).To(ContainSubstring("(one of connect, mitm) (default connect)"))
		Expect(page).To(ContainSubstring("EXPORTS"))
		Expect(page).To(ContainSubstring("HTTPS_PROXY"))
		Expect(page).To(ContainSubstring("--pid PID"), "an agent can attach")
		Expect(help("run", "stacks")).To(ContainSubstring("telltale run stacks [flags] --pid PID"))
		Expect(page).ToNot(ContainSubstring("\x1b["), "help written to a buffer is plain")
	})

	DescribeTable("refuses what it cannot run before making a run store",
		func(kind string, args []string, refusal string) {
			workDir := GinkgoT().TempDir()
			_, _, err := execute(append([]string{"run", kind, "--work-dir", workDir}, args...)...)
			Expect(err).To(MatchError(ContainSubstring(refusal)))
			Expect(filepath.Join(workDir, ".telltale")).ToNot(BeADirectory())
		},
		Entry("a value outside a param's choices", "http", []string{"--seam", "proxy", "--mode", "sideways", "--", "true"}, `--mode: "sideways" is not one of connect, mitm`),
		Entry("params the kind refuses", "sql", []string{"--seam", "proxy", "--dsn", "postgres://localhost/db", "--max-statements", "-1", "--", "true"}, "maxStatements -1 must be at least 1"),
		Entry("a proxy aimed at a running process", "http", []string{"--seam", "proxy", "--pid", strconv.Itoa(os.Getpid())}, "invalid target"),
		Entry("no command and no pid", "stacks", []string{}, "give a command or a pid to attach to"),
		Entry("both a command and a pid", "stacks", []string{"--pid", strconv.Itoa(os.Getpid()), "--", "true"}, "not both"),
		Entry("an invalid agent selector", "stacks", []string{"--selector", "invalid"}, "--selector:"),
	)

	It("runs a command with one kind, recording it in the run's manifest", func() {
		workDir := GinkgoT().TempDir()
		_, stderr, err := execute("run", "http", "--seam", "proxy", "--work-dir", workDir, "--", "true")
		Expect(err).ToNot(HaveOccurred(), stderr)
		Expect(manifest(workDir).Captures).To(ConsistOf(HaveField("Kind", "http")))
	})

	It("attaches to a running process for a duration", func() {
		target := exec.Command("sleep", "30")
		Expect(target.Start()).To(Succeed())
		DeferCleanup(func() {
			_ = target.Process.Kill()
			_ = target.Wait()
		})
		workDir := GinkgoT().TempDir()
		_, stderr, err := execute("run", "process_samples", "--work-dir", workDir, "--pid", strconv.Itoa(target.Process.Pid), "--duration", "1s")
		Expect(err).ToNot(HaveOccurred(), stderr)
		Expect(stderr).To(ContainSubstring("Tracing pid " + strconv.Itoa(target.Process.Pid)))
		result := manifest(workDir)
		Expect(result.PID).To(Equal(target.Process.Pid))
		Expect(result.Captures).To(ConsistOf(HaveField("Kind", "process_samples")))
	})

	It("runs goprof and stacks against a child through the agent seam", func() {
		config := GinkgoT().TempDir()
		child := tracingtest.StartGoChild(tracingtest.GoChildBinary(GinkgoT().TempDir()), "-agent-config", config)
		Expect(agentclient.Default.Discover(context.Background(), filepath.Join(config, "telltale", "agents"))).To(Succeed())
		for _, run := range []struct {
			kind  string
			extra []string
		}{{kind: "goprof", extra: []string{"--seam", "agent"}}, {kind: "goprof", extra: []string{"--seam", "agent", "--profile", "cpu", "--seconds", "1"}}, {kind: "goprof"}, {kind: "stacks", extra: []string{"--seam", "agent"}}} {
			workDir := GinkgoT().TempDir()
			args := append([]string{"run", run.kind, "--work-dir", workDir, "--pid", strconv.Itoa(child.PID), "--duration", "1s"}, run.extra...)
			_, stderr, err := execute(args...)
			Expect(err).NotTo(HaveOccurred(), stderr)
			Expect(manifest(workDir).Captures).To(ContainElement(HaveField("Kind", run.kind)))
		}
	})
	DescribeTable("attaches a run while keeping the kind's seam", func(kind string, extra []string) {
		workDir := GinkgoT().TempDir()
		args := append([]string{"run", kind, "--work-dir", workDir, "--pid", strconv.Itoa(os.Getpid()), "--duration", "300ms"}, extra...)
		_, stderr, err := execute(args...)
		Expect(err).NotTo(HaveOccurred(), stderr)
		Expect(manifest(workDir).PID).To(Equal(os.Getpid()))
	},
		Entry("self", "http", []string{"--seam", "self", "--features", "http"}),
		Entry("host", "process_groups", nil),
	)
	DescribeTable("reports the same refusal for CLI and library runs", func(kind string, args []string, options telltale.StartOptions) {
		_, _, cliErr := execute(append([]string{"run", kind}, args...)...)
		options.Traces = []telltale.TraceSpec{{Kind: kind}}
		_, libraryErr := telltale.Start(context.Background(), options)
		Expect(cliErr).To(HaveOccurred())
		Expect(libraryErr).To(HaveOccurred())
		Expect(cliErr.Error()).To(Equal(libraryErr.Error()))
	},
		Entry("a missing agent", "http", []string{"--pid", strconv.Itoa(os.Getpid())}, telltale.StartOptions{PID: os.Getpid()}),
		Entry("no command or PID", "stacks", nil, telltale.StartOptions{}),
		Entry("a command and PID", "stacks", []string{"--pid", strconv.Itoa(os.Getpid()), "--", "true"}, telltale.StartOptions{PID: os.Getpid(), Command: []string{"true"}}),
	)
	It("shows supported seams and run attachment in help", func() {
		page := help("run", "process_groups")
		Expect(page).To(ContainSubstring("--pid PID"))
		Expect(page).To(ContainSubstring("supported seams: [host]"))
		Expect(page).NotTo(ContainSubstring("--agent"))
		Expect(help("run", "process_samples")).To(ContainSubstring("--seam process"))
	})
})

var _ = Describe("telltale trace run", func() {
	It("lists every registered kind and its params in its help", func() {
		page := help("trace", "run")
		for _, registration := range traces.Registrations() {
			Expect(page).To(ContainSubstring(registration.Name))
		}
		Expect(page).To(ContainSubstring("mode string (connect|mitm)"))
		Expect(page).To(ContainSubstring("HOW IT WORKS"))
		Expect(page).To(ContainSubstring("--trace stringArray"))
	})

	It("refuses an unknown kind before making a run store, naming the registered ones", func() {
		workDir := GinkgoT().TempDir()
		_, _, err := execute("trace", "run", "--work-dir", workDir, "--trace", "nope", "--", "true")
		Expect(err).To(MatchError(And(ContainSubstring(`unknown trace kind "nope" (registered: `), ContainSubstring("http"), ContainSubstring("sql"))))
		Expect(filepath.Join(workDir, ".telltale")).ToNot(BeADirectory())
	})
})

var _ = Describe("presets", func() {
	It("each have a command with their help", func() {
		for _, preset := range telltale.Presets() {
			page := help(preset.Name)
			Expect(page).To(ContainSubstring(preset.Description))
			Expect(page).To(ContainSubstring("telltale " + preset.Name + " [flags] PID"))
		}
		Expect(help("gops")).To(ContainSubstring("stacks"))
	})

	DescribeTable("refuse a target they cannot read",
		func(args []string, refusal string) {
			_, _, err := execute(args...)
			Expect(err).To(MatchError(ContainSubstring(refusal)))
		},
		Entry("both a pid and a command", []string{"ps", "1", "--", "true"}, "not both"),
		Entry("neither", []string{"ps"}, "give the pid of a running process"),
		Entry("a pid that is not a number", []string{"ps", "nope"}, `"nope" is not a pid`),
		Entry("a process without a gops agent", []string{"gops", strconv.Itoa(os.Getpid())}, "runs no gops agent"),
	)

	It("ps follows a running process for a duration", func() {
		target := exec.Command("sleep", "30")
		Expect(target.Start()).To(Succeed())
		DeferCleanup(func() {
			_ = target.Process.Kill()
			_ = target.Wait()
		})
		workDir := GinkgoT().TempDir()
		_, stderr, err := execute("ps", "--work-dir", workDir, "--duration", "1s", strconv.Itoa(target.Process.Pid))
		Expect(err).ToNot(HaveOccurred(), stderr)
		Expect(manifest(workDir).PID).To(Equal(target.Process.Pid))
	})
})
