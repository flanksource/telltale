package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing/fstest"
	"time"

	"github.com/flanksource/clicky/rpc"
	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/internal/jvm"
	"github.com/flanksource/telltale/internal/server"
	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/sdk/protocol"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/spf13/cobra"
)

// jvmCommand is the command at a path of the CLI, such as [jvm patch apply].
func jvmCommand(path ...string) *cobra.Command {
	command, args, err := NewRoot("test").Find(path)
	Expect(err).NotTo(HaveOccurred(), strings.Join(path, " "))
	Expect(args).To(BeEmpty(), strings.Join(path, " "))
	return command
}

// executeJVM runs `telltale jvm <args>` against a recording agent.
func executeJVM(ctx context.Context, args ...string) error {
	root := NewRoot("test")
	root.SetArgs(append([]string{"jvm"}, args...))
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SilenceUsage = true
	return root.ExecuteContext(ctx)
}

// sentJVMRequest runs a generated command against a recording agent and
// returns the request it sent.
func sentJVMRequest(ctx context.Context, args ...string) (jvm.Request, error) {
	var sent jvm.Request
	DeferCleanup(func(previous func(context.Context, jvm.Request) (json.RawMessage, error)) { callJVM = previous }, callJVM)
	callJVM = func(_ context.Context, request jvm.Request) (json.RawMessage, error) {
		sent = request
		return nil, errNotCalled
	}
	if err := executeJVM(ctx, args...); !errors.Is(err, errNotCalled) {
		return sent, err
	}
	return sent, nil
}

// errNotCalled stops a command at the agent call, once its request is assembled.
var errNotCalled = errors.New("agent not called by the test")

var errNotStarted = errors.New("run not started by the test")

// startedTrace runs a kind preset and returns the trace spec and duration it
// would start, without starting a run.
func startedTrace(ctx context.Context, args ...string) (telltale.TraceSpec, time.Duration, error) {
	registry := agentclient.NewRegistry()
	Expect(registry.Add(&agentclient.Agent{Hello: protocol.Hello{AgentID: "a", Runtime: protocol.Runtime{Lang: "java"},
		Probes: []protocol.ProbeSpec{{Name: "jvm_trace"}, {Name: "jvm_jfr"}}}})).To(Succeed())
	// Only the agent added here: no local agents of this machine are discovered.
	registry.Dir = GinkgoT().TempDir()
	DeferCleanup(func(previous *agentclient.Registry) { agentclient.Default = previous }, agentclient.Default)
	agentclient.Default = registry
	var started telltale.StartOptions
	DeferCleanup(func(previous func(context.Context, telltale.StartOptions) (*telltale.Run, error)) {
		startKindRun = previous
	}, startKindRun)
	startKindRun = func(_ context.Context, options telltale.StartOptions) (*telltale.Run, error) {
		started = options
		return nil, errNotStarted
	}
	err := executeJVM(ctx, args...)
	if len(started.Traces) != 1 {
		return telltale.TraceSpec{}, 0, err
	}
	return started.Traces[0], started.Duration, err
}

var _ = Describe("JVM command tree", func() {
	It("serves each operation and kind preset at its declared command", func() {
		served := map[string]string{}
		for _, operation := range jvm.Operations() {
			served[strings.Join(operation.Command, " ")] = operation.Name
		}
		for _, registration := range traces.Registrations() {
			for _, preset := range registration.Commands {
				served[strings.Join(preset.Path, " ")] = registration.Name
			}
		}
		Expect(served).To(Equal(map[string]string{
			"jvm settings": "jvm.settings", "jvm resources": "jvm.resources", "jvm flags": "jvm.flags",
			"jvm decompile": "jvm.classes.source", "jvm decompile bytecode": "jvm.classes.bytecode",
			"jvm patch apply": "jvm.classes.patch", "jvm patch restore": "jvm.classes.restore", "jvm patch list": "jvm.classes.patches",
			"jvm patch compile": "jvm.classes.compile", "jvm patch lease": "jvm.classes.lease",
			"jvm watch": "jvm_trace", "jvm trace": "jvm_trace", "jvm profiler": "jvm_jfr",
			"jvm trace instrument": "jvm.classes.instrument", "jvm trace probes": "jvm.probes.list",
			"jvm trace read": "jvm.probes.read", "jvm trace freeze": "jvm.probes.freeze",
			"jvm classes": "jvm.classes", "jvm loaders": "jvm.loaders", "jvm catalog": "jvm.catalog", "jvm status": "jvm.status",
			"jvm jmx search": "jvm.jmx.search", "jvm jmx info": "jvm.jmx.info", "jvm jmx read": "jvm.jmx.read",
			"jvm jmx write": "jvm.jmx.write", "jvm jmx invoke": "jvm.jmx.invoke", "jvm metrics": "jvm.metrics",
			"jvm threads": "jvm.threads", "jvm threads dump": "jvm.threads.dump", "jvm threads stack": "jvm.threads.stack", "jvm heap histogram": "jvm.heap.histogram",
			"jvm logging list": "jvm.logging.list", "jvm logging set": "jvm.logging.set",
			"jvm groovy eval": "jvm.groovy.eval", "jvm ognl eval": "jvm.ognl.eval",
			"jvm extensions list": "jvm.extensions.list", "jvm extensions install": "jvm.extensions.install",
			"jvm extensions replace": "jvm.extensions.replace", "jvm extensions unload": "jvm.extensions.unload",
			"jvm notifications list": "jvm.notifications.list",
		}))
		for command := range served {
			Expect(jvmCommand(strings.Fields(command)...).RunE).NotTo(BeNil(), command)
		}
	})

	It("keeps a runnable preset beside the operations declared below it", func() {
		trace := jvmCommand("jvm", "trace")
		Expect(trace.Flags().Lookup("targets")).NotTo(BeNil())
		var children []string
		for _, child := range trace.Commands() {
			children = append(children, child.Name())
		}
		Expect(children).To(ConsistOf("instrument", "probes", "read", "freeze"))
		for _, removed := range [][]string{{"jvm", "classes", "source"}, {"jvm", "classes", "patch"}, {"jvm", "probes"}} {
			_, args, err := NewRoot("test").Find(removed)
			Expect(err != nil || len(args) > 0).To(BeTrue(), strings.Join(removed, " "))
		}
	})

	It("starts the preset's kind with its defaults under the flags given", func() {
		cases := []struct {
			args     []string
			kind     string
			expected string
		}{
			{[]string{"watch", "--agent", "a", "--targets", `[{"class":"Idle","method":"main"}]`, "--duration", "3s"}, "jvm_trace",
				`{"args":true,"return":true,"exception":true,"calls":false,"targets":[{"class":"Idle","method":"main"}],"target":{"agent":"a"}}`},
			{[]string{"trace", "--agent", "a", "--targets", `[{"class":"Idle","method":"main"}]`, "--duration", "3s"}, "jvm_trace",
				`{"args":true,"return":true,"exception":true,"calls":true,"targets":[{"class":"Idle","method":"main"}],"target":{"agent":"a"}}`},
			{[]string{"trace", "--agent", "a", "--targets", `[{"class":"Idle","method":"main"}]`, "--calls=false", "--args=false", "--duration", "3s"}, "jvm_trace",
				`{"args":false,"return":true,"exception":true,"calls":false,"targets":[{"class":"Idle","method":"main"}],"target":{"agent":"a"}}`},
			{[]string{"profiler", "--agent", "a", "--duration", "3s"}, "jvm_jfr", `{"target":{"seam":"agent","agent":"a"}}`},
		}
		for _, c := range cases {
			spec, duration, err := startedTrace(context.Background(), c.args...)
			Expect(err).To(MatchError(errNotStarted), strings.Join(c.args, " "))
			Expect(duration).To(Equal(3*time.Second), strings.Join(c.args, " "))
			Expect(spec.Kind).To(Equal(c.kind), strings.Join(c.args, " "))
			Expect(spec.Params).To(MatchJSON(c.expected), strings.Join(c.args, " "))
		}
	})

	It("names the agent a capture runs on by its id, from a short name or from the only agent offering the kind", func() {
		GinkgoT().Setenv("XDG_CONFIG_HOME", GinkgoT().TempDir())
		cases := []struct {
			args     []string
			expected string
		}{
			{[]string{"watch", "--targets", `[{"class":"Idle","method":"main"}]`, "--duration", "3s"}, `{"args":true,"return":true,"exception":true,"calls":false,"targets":[{"class":"Idle","method":"main"}],"target":{"agent":"a"}}`},
			{[]string{"profiler", "--duration", "3s"}, `{"target":{"seam":"agent","agent":"a"}}`},
		}
		for _, c := range cases {
			spec, _, err := startedTrace(context.Background(), c.args...)
			Expect(err).To(MatchError(errNotStarted), strings.Join(c.args, " "))
			Expect(spec.Params).To(MatchJSON(c.expected), strings.Join(c.args, " "))
		}
		_, _, err := startedTrace(context.Background(), "watch", "--agent", "billing", "--targets", `[{"class":"Idle","method":"main"}]`, "--duration", "3s")
		Expect(err).To(MatchError(`no agent offering jvm_trace matches "billing"; agents: a ()`))
	})

	It("sends a preset capture requested over HTTP to the server's run API", func() {
		remote := rpc.ContextWithRequest(context.Background(), httptest.NewRequest("POST", "/api/v1/jvm/watch", nil))
		_, _, err := startedTrace(remote, "watch", "--agent", "a", "--targets", `[{"class":"Idle","method":"main"}]`, "--duration", "3s")
		Expect(err).To(MatchError("telltale jvm watch records a run: start it over HTTP with POST /api/traces and kind jvm_trace"))
	})
})

var _ = Describe("typed JVM operation commands", func() {
	It("generates a command with a flag per param for every built-in operation", func() {
		for _, operation := range jvm.Operations() {
			command := jvmCommand(operation.Command...)
			Expect(command.RunE).NotTo(BeNil(), operation.Name)
			Expect(command.Short).To(Equal(operation.Description))
			Expect(command.Flags().Lookup("agent")).NotTo(BeNil(), operation.Name)
			Expect(command.Flags().Lookup("allow-mutation") != nil).To(Equal(operation.Mutates), operation.Name)
			for name := range operation.Params.Properties {
				Expect(command.Flags().Lookup(flagName([]string{name}))).NotTo(BeNil(), operation.Name+" "+name)
			}
		}
	})

	It("keeps an operation that is also a group runnable beside its subcommands", func() {
		decompile := jvmCommand("jvm", "decompile")
		Expect(decompile.Flags().Lookup("class")).NotTo(BeNil())
		Expect(jvmCommand("jvm", "decompile", "bytecode").Short).To(Equal("Read a class's bytecode as base64"))
	})

	It("reaches a reorganized operation through its new command", func() {
		request, err := sentJVMRequest(context.Background(), "patch", "list", "--agent", "a")
		Expect(err).NotTo(HaveOccurred())
		Expect(request.Operation).To(Equal("jvm.classes.patches"))
		request, err = sentJVMRequest(context.Background(), "trace", "read", "--agent", "a", "--key", "k", "--limit", "5")
		Expect(err).NotTo(HaveOccurred())
		Expect(request.Operation).To(Equal("jvm.probes.read"))
		Expect(request.Params).To(MatchJSON(`{"key":"k","limit":5}`))
	})

	It("sends typed params as the operation's JSON params", func() {
		request, err := sentJVMRequest(context.Background(), "jmx", "write", "--agent", "java/demo", "--allow-mutation",
			"--object-name", "example:type=Cache", "--attribute", "Size", "--value", `{"limit":9007199254740993}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(request.Agent).To(Equal("java/demo"))
		Expect(request.Operation).To(Equal("jvm.jmx.write"))
		Expect(request.AllowMutation).To(BeTrue())
		Expect(request.Params).To(MatchJSON(`{"objectName":"example:type=Cache","attribute":"Size","value":{"limit":9007199254740993}}`))
	})

	It("sends typed lists, integers and an explicit null", func() {
		request, err := sentJVMRequest(context.Background(), "jmx", "invoke", "--agent", "a", "--object-name", "o", "--operation", "op",
			"--args", `[1,"two"]`, "--signature", "long,java.lang.String")
		Expect(err).NotTo(HaveOccurred())
		Expect(request.Params).To(MatchJSON(`{"objectName":"o","operation":"op","args":[1,"two"],"signature":["long","java.lang.String"]}`))
		request, err = sentJVMRequest(context.Background(), "threads", "stack", "--agent", "a", "--id", "7")
		Expect(err).NotTo(HaveOccurred())
		Expect(request.Params).To(MatchJSON(`{"id":7}`))
		request, err = sentJVMRequest(context.Background(), "logging", "set", "--agent", "a", "--name", "", "--level", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(request.Params).To(MatchJSON(`{"name":"","level":null}`))
	})

	It("leaves an omitted --agent to the call, which picks the only Java agent", func() {
		request, err := sentJVMRequest(context.Background(), "jmx", "search", "--pattern", "java.lang:*")
		Expect(err).NotTo(HaveOccurred())
		Expect(request.Agent).To(BeEmpty())
	})

	DescribeTable("refuses invalid input before calling the agent", func(message string, args ...string) {
		_, err := sentJVMRequest(context.Background(), args...)
		Expect(err).To(MatchError(ContainSubstring(message)))
	},
		Entry("a missing required param", "object-name", "jmx", "info", "--agent", "a"),
		Entry("a non-array list", "expected a JSON array", "jmx", "invoke", "--agent", "a", "--object-name", "o", "--operation", "op", "--args", `{}`),
		Entry("a file and an inline value together", "none of the others", "groovy", "eval", "--agent", "a", "--source", "1", "--source-file", "x.groovy"),
		Entry("neither a value nor its file", "at least one of the flags", "extensions", "install", "--agent", "a"),
	)

	It("reads source as text and a JAR as base64 from local files", func() {
		dir := GinkgoT().TempDir()
		script, jar := filepath.Join(dir, "inspect.groovy"), filepath.Join(dir, "example.jar")
		Expect(os.WriteFile(script, []byte("return args.id"), 0o600)).To(Succeed())
		Expect(os.WriteFile(jar, []byte{0x50, 0x4b, 0x03, 0x04}, 0o600)).To(Succeed())
		request, err := sentJVMRequest(context.Background(), "groovy", "eval", "--agent", "a", "--allow-mutation", "--source-file", script, "--args", `{"id":9007199254740993}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(request.Params).To(MatchJSON(`{"source":"return args.id","args":{"id":9007199254740993}}`))
		request, err = sentJVMRequest(context.Background(), "extensions", "install", "--agent", "a", "--allow-mutation", "--jar-file", jar)
		Expect(err).NotTo(HaveOccurred())
		Expect(request.Params).To(MatchJSON(`{"jar":"` + base64.StdEncoding.EncodeToString([]byte{0x50, 0x4b, 0x03, 0x04}) + `"}`))
	})

	It("prints a failed compile's diagnostics and exits non-zero", func() {
		DeferCleanup(func(previous func(context.Context, jvm.Request) (json.RawMessage, error)) { callJVM = previous }, callJVM)
		callJVM = func(context.Context, jvm.Request) (json.RawMessage, error) {
			return json.RawMessage(`{"success":false,"classes":[],"diagnostics":[{"kind":"ERROR","line":3,"column":57,"code":"compiler.err.expected","message":"';' expected"}],"classpathEntries":2}`), nil
		}
		root := NewRoot("test")
		var out bytes.Buffer
		root.SetArgs([]string{"jvm", "patch", "compile", "--agent", "a", "--class", "Idle", "--source", "static void check(long l) {}"})
		root.SetOut(&out)
		root.SetErr(&bytes.Buffer{})
		Expect(root.ExecuteContext(context.Background())).To(MatchError("compile failed with 1 error"))
		Expect(out.String()).To(Equal("✗ compile failed · 1 error · 2 classpath entries\nERROR 3:57 ';' expected (compiler.err.expected)\n"))
	})

	It("executes typed operations over HTTP and refuses to read server files for them", func() {
		var sent jvm.Request
		DeferCleanup(func(previous func(context.Context, jvm.Request) (json.RawMessage, error)) { callJVM = previous }, callJVM)
		callJVM = func(_ context.Context, request jvm.Request) (json.RawMessage, error) {
			sent = request
			return json.RawMessage(`["java.lang:type=Memory"]`), nil
		}
		handler, err := server.NewHandler(server.HandlerOptions{
			Root: NewRoot("test"), Assets: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}},
			Traces: http.NotFoundHandler(), Top: http.NotFoundHandler(),
		})
		Expect(err).NotTo(HaveOccurred())
		post := func(path, body string) *httptest.ResponseRecorder {
			request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Accept", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			return response
		}
		response := post("/api/v1/jvm/jmx/search", `{"agent":"java/demo","pattern":"java.lang:*"}`)
		Expect(response.Code).To(Equal(http.StatusOK), response.Body.String())
		Expect(sent.Operation).To(Equal("jvm.jmx.search"))
		Expect(sent.Params).To(MatchJSON(`{"pattern":"java.lang:*"}`))
		response = post("/api/v1/jvm/extensions/install", `{"agent":"java/demo","allow-mutation":true,"jar-file":"/etc/hosts"}`)
		Expect(response.Body.String()).To(ContainSubstring("send jar in the request instead"))
	})

	It("refuses to read a local file for a request that arrived over HTTP", func() {
		jar := filepath.Join(GinkgoT().TempDir(), "example.jar")
		Expect(os.WriteFile(jar, []byte("jar"), 0o600)).To(Succeed())
		remote := rpc.ContextWithRequest(context.Background(), httptest.NewRequest("POST", "/api/v1/jvm/extensions/install", nil))
		_, err := sentJVMRequest(remote, "extensions", "install", "--agent", "a", "--jar-file", jar)
		Expect(err).To(MatchError(ContainSubstring("send jar in the request instead")))
	})
})
