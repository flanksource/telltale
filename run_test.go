package telltale_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/tracing/groups"
	"github.com/flanksource/telltale/pkg/tracing/httpproxy"
	"github.com/flanksource/telltale/pkg/tracing/process"
	"github.com/flanksource/telltale/pkg/tracing/stacks"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("parsing a trace spec", func() {
	DescribeTable("reads kind or kind:{json}",
		func(value string, expected telltale.TraceSpec) {
			Expect(telltale.ParseTraceSpec(value)).To(Equal(expected))
		},
		Entry("bare kind", "http", telltale.TraceSpec{Kind: "http"}),
		Entry("kind with params", `http:{"mode":"mitm"}`, telltale.TraceSpec{Kind: "http", Params: json.RawMessage(`{"mode":"mitm"}`)}),
	)
	DescribeTable("refuses",
		func(value, expected string) {
			_, err := telltale.ParseTraceSpec(value)
			Expect(err).To(MatchError(ContainSubstring(expected)))
		},
		Entry("no kind", `:{"mode":"mitm"}`, "names no kind"),
		Entry("params that are not JSON", "http:mode=mitm", "not JSON"),
	)
})

var _ = Describe("running a command under trace", func() {
	run := func(command []string, specs ...telltale.TraceSpec) (*telltale.Result, string) {
		output := &bytes.Buffer{}
		started, err := telltale.Start(context.Background(), telltale.StartOptions{
			WorkDir: GinkgoT().TempDir(), Command: command, Traces: specs, Stdout: output, Stderr: output,
		})
		Expect(err).ToNot(HaveOccurred())
		result, err := started.Wait()
		Expect(err).ToNot(HaveOccurred(), output.String())
		return result, output.String()
	}

	It("reports the command's exit code, its timing and the process tree it recorded", func() {
		before := time.Now()
		result, _ := run([]string{"sh", "-c", "exit 3"})
		Expect(result.ExitCode).To(Equal(3))
		Expect(result.Dir).To(BeADirectory())
		Expect(result.StartedAt).To(BeTemporally(">=", before))
		Expect(result.EndedAt).To(BeTemporally(">=", result.StartedAt))
		Expect(result.Process.Tree.Kind).To(Equal(process.TreeKind))
		Expect(result.Captures).To(BeEmpty())
	})

	It("points the command at a recording proxy and summarizes the exchanges it relayed", func() {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("traced"))
		}))
		DeferCleanup(upstream.Close)

		result, output := run([]string{"curl", "-sS", upstream.URL + "/hello"}, telltale.TraceSpec{Kind: httpproxy.Kind, Params: json.RawMessage(`{"target":{"seam":"proxy"}}`)})
		Expect(result.ExitCode).To(Equal(0), output)
		Expect(output).To(Equal("traced"))
		Expect(result.Captures).To(HaveLen(1))
		Expect(result.Captures[0].Kind).To(Equal(httpproxy.Kind))
		Expect(result.Captures[0].Summary).To(HaveKeyWithValue("count", BeNumerically("==", 1)))
	})

	It("refuses a kind the catalog does not serve before running anything", func() {
		_, err := telltale.Start(context.Background(), telltale.StartOptions{
			WorkDir: GinkgoT().TempDir(), Command: []string{"true"}, Traces: []telltale.TraceSpec{{Kind: "nosuchkind"}},
		})
		Expect(err).To(MatchError(ContainSubstring(`unknown trace kind "nosuchkind"`)))
	})
})

// sleeper is a running process, reaped once it exits, for a run to attach to.
func sleeper(seconds string) *exec.Cmd {
	cmd := exec.Command("sleep", seconds)
	Expect(cmd.Start()).To(Succeed())
	reaped := make(chan struct{})
	go func() {
		_ = cmd.Wait() // killed below or exited; either way reaped, never a zombie
		close(reaped)
	}()
	DeferCleanup(func() {
		_ = cmd.Process.Kill()
		<-reaped
	})
	return cmd
}

var _ = Describe("a run attached to a running process", func() {
	It("traces the process's tree until its duration passes, leaving the process running", func() {
		target, workDir := sleeper("30"), GinkgoT().TempDir()
		run, err := telltale.Start(context.Background(), telltale.StartOptions{
			WorkDir: workDir, PID: target.Process.Pid, Duration: time.Second,
			Traces: []telltale.TraceSpec{{Kind: process.SamplesKind}},
		})
		Expect(err).ToNot(HaveOccurred())
		result, err := run.Wait()
		Expect(err).ToNot(HaveOccurred())
		Expect(result.PID).To(Equal(target.Process.Pid))
		Expect(result.Command).To(BeNil())
		Expect(result.EndedAt.Sub(result.StartedAt)).To(BeNumerically(">=", time.Second))
		Expect(result.Captures).To(ConsistOf(HaveField("Kind", process.SamplesKind)))
		Expect(target.Process.Signal(syscall.Signal(0))).To(Succeed(), "an attached run leaves its process running")

		info, err := telltale.Get(telltale.GetOptions{WorkDir: workDir, RunID: result.RunID})
		Expect(err).ToNot(HaveOccurred())
		Expect(info.Status).To(Equal(telltale.StatusSucceeded))
		Expect(info.PID).To(Equal(target.Process.Pid))
	})

	It("ends when the process exits", func() {
		target := sleeper("1")
		run, err := telltale.Start(context.Background(), telltale.StartOptions{WorkDir: GinkgoT().TempDir(), PID: target.Process.Pid})
		Expect(err).ToNot(HaveOccurred())
		Eventually(run.Done(), 10*time.Second).Should(BeClosed())
	})

	It("dumps an attached process's stacks as it stops", func() {
		target := sleeper("30")
		run, err := telltale.Start(context.Background(), telltale.StartOptions{
			WorkDir: GinkgoT().TempDir(), PID: target.Process.Pid, Duration: 500 * time.Millisecond,
			Traces: []telltale.TraceSpec{{Kind: stacks.Kind}},
		})
		Expect(err).ToNot(HaveOccurred())
		result, err := run.Wait()
		Expect(err).ToNot(HaveOccurred())
		Expect(result.CheckpointErrors).To(BeEmpty())
		Expect(result.Captures).To(ConsistOf(And(HaveField("Kind", stacks.Kind), HaveField("Summary", HaveKeyWithValue("count", BeNumerically(">", 0))))))
	})

	It("records the checkpoints that fail without ending the run, and fails it", func() {
		target, workDir := sleeper("30"), GinkgoT().TempDir()
		run, err := telltale.Start(context.Background(), telltale.StartOptions{
			WorkDir: workDir, PID: target.Process.Pid, Duration: 500 * time.Millisecond,
			Traces: []telltale.TraceSpec{{Kind: stacks.Kind, Checkpoint: json.RawMessage(`{"transport": "gops"}`)}},
		})
		Expect(err).ToNot(HaveOccurred())
		result, err := run.Wait()
		Expect(err).ToNot(HaveOccurred())
		Expect(result.CheckpointErrors).To(ConsistOf(ContainSubstring("stacks stop checkpoint")))
		info, err := telltale.Get(telltale.GetOptions{WorkDir: workDir, RunID: result.RunID})
		Expect(err).ToNot(HaveOccurred())
		Expect(info.Status).To(Equal(telltale.StatusFailed))
	})

	DescribeTable("refuses what it cannot attach to before opening a store",
		func(options func(pid int) telltale.StartOptions, refusal string) {
			workDir := GinkgoT().TempDir()
			started := options(sleeper("30").Process.Pid)
			started.WorkDir = workDir
			_, err := telltale.Start(context.Background(), started)
			Expect(err).To(MatchError(ContainSubstring(refusal)))
			Expect(filepath.Join(workDir, ".telltale")).ToNot(BeADirectory())
		},
		Entry("a proxy the command is pointed at", func(pid int) telltale.StartOptions {
			return telltale.StartOptions{PID: pid, Traces: []telltale.TraceSpec{{Kind: httpproxy.Kind}}}
		}, "no agent advertises probe http"),
		Entry("both a command and a pid", func(pid int) telltale.StartOptions {
			return telltale.StartOptions{PID: pid, Command: []string{"true"}}
		}, "not both"),
		Entry("neither", func(int) telltale.StartOptions { return telltale.StartOptions{} }, "a command, a pid to attach to, or a host capture is required"),
		Entry("neither, for a kind that observes a process", func(int) telltale.StartOptions {
			return telltale.StartOptions{Traces: []telltale.TraceSpec{{Kind: groups.Kind}, {Kind: process.SamplesKind}}}
		}, "process_samples observes a command or a process: give a command or a pid"),
		Entry("a process that is not running", func(int) telltale.StartOptions {
			return telltale.StartOptions{PID: 999999}
		}, "pid 999999 is not running"),
	)
})

var _ = Describe("a host run", func() {
	It("records the host's process groups with neither a command nor a process until its duration passes", func() {
		workDir := GinkgoT().TempDir()
		run, err := telltale.Start(context.Background(), telltale.StartOptions{
			WorkDir: workDir, Duration: 2500 * time.Millisecond,
			Traces: []telltale.TraceSpec{{Kind: groups.Kind, Params: json.RawMessage(`{"interval": "1s", "all": true}`)}},
		})
		Expect(err).ToNot(HaveOccurred())
		info := run.Info()
		Expect(info).To(And(HaveField("Status", telltale.StatusRunning), HaveField("Process", BeNil()), HaveField("Live", BeNil())))
		follower, err := groups.NewFollower(run.Runtime(), info.Captures[0].Ref)
		Expect(err).ToNot(HaveOccurred())
		Eventually(func(g Gomega) {
			frame, ok, err := follower.Frame(context.Background())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(ok).To(BeTrue())
			g.Expect(frame.Groups).ToNot(BeEmpty())
			g.Expect(frame.Groups).To(HaveEach(HaveField("Tick", frame.Tick)), "a frame holds one tick")
		}).WithTimeout(2 * time.Second).WithPolling(200 * time.Millisecond).Should(Succeed())

		result, err := run.Wait()
		Expect(err).ToNot(HaveOccurred())
		Expect(result.EndedAt.Sub(result.StartedAt)).To(BeNumerically(">=", 2500*time.Millisecond))
		Expect(result.Process).To(BeNil())
		Expect(result.Captures).To(ConsistOf(And(HaveField("Kind", groups.Kind), HaveField("Summary", HaveKeyWithValue("count", BeNumerically(">", 0))))))

		stored, err := telltale.Get(telltale.GetOptions{WorkDir: workDir, RunID: result.RunID})
		Expect(err).ToNot(HaveOccurred())
		Expect(stored).To(And(HaveField("Status", telltale.StatusSucceeded), HaveField("Process", BeNil())))
	})
})

var _ = Describe("a run of a command with checkpointed captures", func() {
	It("reads them of the command every interval", func() {
		run, err := telltale.Start(context.Background(), telltale.StartOptions{
			WorkDir: GinkgoT().TempDir(), Command: []string{"sleep", "3"}, Every: time.Second, TraceRoot: telltale.TraceRootCommand,
			Traces: []telltale.TraceSpec{{Kind: stacks.Kind}},
		})
		Expect(err).ToNot(HaveOccurred())
		result, err := run.Wait()
		Expect(err).ToNot(HaveOccurred())
		Expect(result.CheckpointErrors).To(BeEmpty())
		Expect(result.Captures).To(ConsistOf(HaveField("Summary", HaveKeyWithValue("count", BeNumerically(">", 0)))))
	})
})

var _ = Describe("a run traced from its command", func() {
	It("is live until stopped, then records the command's output and how it ended", func() {
		workDir := GinkgoT().TempDir()
		run, err := telltale.Start(context.Background(), telltale.StartOptions{
			WorkDir: workDir, Dir: workDir, Command: []string{"sh", "-c", "echo started; sleep 30"}, TraceRoot: telltale.TraceRootCommand,
		})
		Expect(err).ToNot(HaveOccurred())

		info, err := telltale.Get(telltale.GetOptions{WorkDir: workDir, RunID: run.ID()})
		Expect(err).ToNot(HaveOccurred())
		Expect(info.Status).To(Equal(telltale.StatusRunning))
		Expect(info.Live).ToNot(BeNil())
		Expect(info.Live.Sessions.Tree).ToNot(BeEmpty())

		Eventually(func() (string, error) {
			data, err := os.ReadFile(filepath.Join(run.Dir(), telltale.OutputFile))
			return string(data), err
		}).Should(Equal("started\n"))
		Expect(telltale.Stop(telltale.StopOptions{WorkDir: workDir, RunID: run.ID()})).To(Succeed())

		result, err := run.Wait()
		Expect(err).ToNot(HaveOccurred())
		Expect(result.ExitCode).ToNot(Equal(0))
		Expect(filepath.Join(run.Dir(), telltale.ManifestFile)).To(BeARegularFile())

		info, err = telltale.Get(telltale.GetOptions{WorkDir: workDir, RunID: run.ID()})
		Expect(err).ToNot(HaveOccurred())
		Expect(info.Status).To(Equal(telltale.StatusFailed))
		Expect(info.Live).To(BeNil())
		Expect(telltale.Stop(telltale.StopOptions{WorkDir: workDir, RunID: run.ID()})).To(MatchError(telltale.ErrNotLive))
	})

	It("is stopped when the context it was started under ends", func() {
		ctx, cancel := context.WithCancel(context.Background())
		run, err := telltale.Start(ctx, telltale.StartOptions{
			WorkDir: GinkgoT().TempDir(), Command: []string{"sleep", "30"}, TraceRoot: telltale.TraceRootCommand,
		})
		Expect(err).ToNot(HaveOccurred())
		cancel()
		Eventually(run.Done(), 10*time.Second).Should(BeClosed())
		result, err := run.Wait()
		Expect(err).ToNot(HaveOccurred())
		Expect(result.ExitCode).ToNot(Equal(0))
	})
})
