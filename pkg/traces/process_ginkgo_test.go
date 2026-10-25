package traces_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/traceserve"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/process"
	"github.com/flanksource/telltale/pkg/tracing/stacks"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// served is a host's store served through traceserve, as the UI mounts it.
type served struct{ server *httptest.Server }

func serveHost(host *traces.Host) served {
	handler, err := traceserve.Handler(host.Runtime())
	Expect(err).ToNot(HaveOccurred())
	server := httptest.NewServer(handler)
	DeferCleanup(server.Close)
	return served{server: server}
}

func (s served) post(path, body string) int {
	response, err := s.server.Client().Post(s.server.URL+path, "application/json", strings.NewReader(body))
	Expect(err).ToNot(HaveOccurred())
	Expect(response.Body.Close()).To(Succeed())
	return response.StatusCode
}

// records exports ref's window: the records as the stream holds them.
func (s served) records(ref tracing.TraceRef) []map[string]any {
	query := url.Values{"format": {"ndjson"}, "stream": {ref.Stream}}
	response, err := s.server.Client().Get(s.server.URL + "/api/v1/traces/" + ref.Kind + "/export?" + query.Encode())
	Expect(err).ToNot(HaveOccurred())
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	Expect(err).ToNot(HaveOccurred())
	if response.StatusCode == http.StatusNotFound {
		return nil
	}
	Expect(response.StatusCode).To(Equal(http.StatusOK), string(body))
	var records []map[string]any
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	scanner.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for scanner.Scan() {
		var record map[string]any
		Expect(json.Unmarshal(scanner.Bytes(), &record)).To(Succeed())
		records = append(records, record)
	}
	return records
}

func byCommand(records []map[string]any) map[string]map[string]any {
	commands := map[string]map[string]any{}
	for _, record := range records {
		commands[record["command"].(string)] = record
	}
	return commands
}

// startShell starts a shell whose child sleeps, in its own process group.
func startShell() int {
	cmd := exec.Command("sh", "-c", "sleep 30 & wait")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = append(os.Environ(), "TELLTALE_TRACE_PROBE=1")
	Expect(cmd.Start()).To(Succeed())
	root := cmd.Process.Pid
	go func() { _ = cmd.Wait() }()
	DeferCleanup(func() { _ = syscall.Kill(-root, syscall.SIGKILL) })
	return root
}

func startTrace(host *traces.Host, options traces.ProcessTraceOptions) *traces.ProcessTrace {
	trace, err := host.StartProcessTrace(options)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() { Expect(trace.Stop(context.Background())).To(Succeed()) })
	return trace
}

var _ = Describe("process trace of a root that exited", func() {
	It("checkpoints its tree as already committed, its last observation taken as it ended", func() {
		root := exec.Command("sleep", "0.3")
		Expect(root.Start()).To(Succeed())
		host := openHost(GinkgoT().TempDir())
		trace, err := host.StartProcessTrace(traces.ProcessTraceOptions{RootPID: root.Process.Pid, Interval: process.MinInterval})
		Expect(err).ToNot(HaveOccurred())
		Expect(root.Wait()).To(Succeed())
		Eventually(func() bool { return trace.Sessions().Tree != "" && treeEnded(host, trace) }, 10*time.Second).Should(BeTrue())
		Expect(trace.CheckpointTree(context.Background())).To(Succeed())
		Expect(trace.Stop(context.Background())).To(Succeed())
	})
})

// treeEnded reports whether the trace's tree session ended by itself.
func treeEnded(host *traces.Host, trace *traces.ProcessTrace) bool {
	session, ok := host.Runtime().Sessions.Get(trace.Sessions().Tree)
	return ok && session.Snapshot().State.Terminal()
}

var _ = Describe("process trace", func() {
	It("serves the tree under the root and its processes' samples through query, reading no facet unarmed", func() {
		root := startShell()
		host := openHost(GinkgoT().TempDir())
		trace := startTrace(host, traces.ProcessTraceOptions{RootPID: root})
		refs := trace.Refs()
		Expect([]string{refs.Tree.Kind, refs.Samples.Kind, refs.Stacks.Kind}).To(Equal([]string{process.TreeKind, process.SamplesKind, stacks.Kind}))
		store := serveHost(host)

		Eventually(func() map[string]map[string]any { return byCommand(store.records(refs.Tree)) }).WithTimeout(5 * time.Second).
			Should(HaveKeyWithValue("sleep 30", HaveKeyWithValue("ppid", BeEquivalentTo(root))))
		sleep := byCommand(store.records(refs.Tree))["sleep 30"]
		Expect(sleep).To(HaveKeyWithValue("environment", BeNil()))
		Eventually(func() []map[string]any { return store.records(refs.Samples) }).WithTimeout(5 * time.Second).
			Should(ContainElement(HaveKeyWithValue("key", sleep["key"])))
		Expect(store.records(refs.Samples)).To(HaveEach(HaveKeyWithValue("openFiles", BeNil())))
	})

	It("arms every facet and reads each process's environment when watched in a UI", func() {
		root := startShell()
		host := openHost(GinkgoT().TempDir())
		trace := startTrace(host, traces.ProcessTraceOptions{RootPID: root, Facets: true})
		store := serveHost(host)

		Eventually(func() map[string]map[string]any { return byCommand(store.records(trace.Refs().Tree)) }).WithTimeout(5 * time.Second).
			Should(HaveKeyWithValue("sleep 30", HaveKeyWithValue("environment", HaveKeyWithValue("TELLTALE_TRACE_PROBE", "1"))))
		Eventually(func() []map[string]any { return store.records(trace.Refs().Samples) }).WithTimeout(5 * time.Second).
			Should(ContainElement(And(
				HaveKeyWithValue("openFiles", BeNumerically(">", 0)), HaveKeyWithValue("openFileList", Not(BeEmpty())),
				HaveKeyWithValue("sockets", Not(BeNil())), HaveKeyWithValue("ports", Not(BeNil())), HaveKeyWithValue("connections", Not(BeNil())),
			)))
	})

	It("dumps the run's own goroutines into stacks through the session it names", func() {
		host := openHost(GinkgoT().TempDir())
		trace := startTrace(host, traces.ProcessTraceOptions{RootPID: os.Getpid()})
		store := serveHost(host)

		Expect(store.post("/api/v1/sessions/"+trace.Sessions().Stacks+"/checkpoint", `{"transport": "inprocess", "reason": "manual"}`)).To(Equal(http.StatusNoContent))
		Expect(store.records(trace.Refs().Stacks)).To(ContainElement(And(
			HaveKeyWithValue("pid", BeEquivalentTo(os.Getpid())),
			HaveKeyWithValue("transport", stacks.TransportInProcess),
			HaveKeyWithValue("reason", "manual"),
			HaveKeyWithValue("frames", ContainElement(HaveKeyWithValue("func", ContainSubstring("traces_test")))),
		)))
	})

	It("records a dump taken elsewhere on the process it names, with its reason", func() {
		root := startShell()
		host := openHost(GinkgoT().TempDir())
		trace := startTrace(host, traces.ProcessTraceOptions{RootPID: root})
		store := serveHost(host)
		Eventually(func() map[string]map[string]any { return byCommand(store.records(trace.Refs().Tree)) }).WithTimeout(5 * time.Second).
			Should(HaveKey("sleep 30"))
		key := byCommand(store.records(trace.Refs().Tree))["sleep 30"]["key"].(string)

		dump := "goroutine 1 [chan receive]:\nmain.main()\n\t/src/main.go:7 +0x1d\n"
		Expect(trace.CheckpointStacks(context.Background(), stacks.CheckpointParams{
			Key: key, Reason: "package-timeout:./pkg",
			Dump: &stacks.Dump{Format: stacks.FormatGo, Text: dump, CollectedAt: time.Now().UTC()},
		})).To(Succeed())
		Expect(store.records(trace.Refs().Stacks)).To(ConsistOf(And(
			HaveKeyWithValue("key", key),
			HaveKeyWithValue("goroutineId", BeEquivalentTo(1)),
			HaveKeyWithValue("state", "chan receive"),
			HaveKeyWithValue("reason", "package-timeout:./pkg"),
		)))
	})
})
