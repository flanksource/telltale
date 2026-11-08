package definition_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/flanksource/commons-db/connection/store"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/definition"
	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/tracing/goprof"
	"github.com/flanksource/telltale/pkg/tracing/processcapture"
	"github.com/flanksource/telltale/pkg/tracing/stacks"
	"github.com/flanksource/telltale/pkg/tracing/tracingtest"
)

// builtin is the built-in definition name.
func builtin(name string) *definition.TraceDefinition {
	defs, err := definition.Builtins()
	Expect(err).ToNot(HaveOccurred())
	for _, def := range defs {
		if def.Name == name {
			return def
		}
	}
	Fail("no built-in definition " + name)
	return nil
}

// start runs def with options, as `telltale <name>` does.
func start(def *definition.TraceDefinition, options telltale.StartOptions) (*telltale.Run, error) {
	applied, err := definition.Apply(def, options)
	if err != nil {
		return nil, err
	}
	return telltale.Start(context.Background(), applied)
}

// sleeper is a process that sleeps until the spec ends.
func sleeper() *exec.Cmd {
	cmd := exec.Command("sleep", "30")
	Expect(cmd.Start()).To(Succeed())
	reaped := make(chan struct{})
	go func() {
		_ = cmd.Wait() // killed below or exited; either way reaped
		close(reaped)
	}()
	DeferCleanup(func() {
		_ = cmd.Process.Kill()
		<-reaped
	})
	return cmd
}

var _ = Describe("the built-in definitions", func() {
	It("each have the summary, description and examples their command's help shows", func() {
		defs, err := definition.Builtins()
		Expect(err).ToNot(HaveOccurred())
		Expect(defs).To(HaveExactElements(HaveField("Name", "gops"), HaveField("Name", "ps")))
		for _, def := range defs {
			Expect(def.Summary()).ToNot(Equal(def.Spec.Description), def.Name)
			Expect(def.Spec.Description).ToNot(BeEmpty(), def.Name)
			Expect(def.Examples()).ToNot(BeEmpty(), def.Name)
			Expect(definition.Validate(def, definition.Options{})).To(Succeed(), def.Name)
		}
	})

	It("ps follows an attached process's tree with every facet", func() {
		target := sleeper()
		run, err := start(builtin("ps"), telltale.StartOptions{DataDir: GinkgoT().TempDir(), PID: target.Process.Pid, Duration: time.Second})
		Expect(err).ToNot(HaveOccurred())
		result, err := run.Wait()
		Expect(err).ToNot(HaveOccurred())
		Expect(result.PID).To(Equal(target.Process.Pid))
		Expect(result.Process.Tree.Kind).To(Equal(processcapture.Kind))
		Expect(result.CheckpointErrors).To(BeEmpty())
	})

	It("gops refuses a process that runs no gops agent before making a run store, naming the port file", func() {
		gopsDir := GinkgoT().TempDir()
		GinkgoT().Setenv("GOPS_CONFIG_DIR", gopsDir)
		target := sleeper()
		dataDir := GinkgoT().TempDir()
		_, err := start(builtin("gops"), telltale.StartOptions{DataDir: dataDir, PID: target.Process.Pid})
		Expect(err).To(MatchError(ContainSubstring("runs no gops agent: no " + filepath.Join(gopsDir, strconv.Itoa(target.Process.Pid)))))
		Expect(os.ReadDir(dataDir)).To(BeEmpty())
	})

	It("gops reads a Go process's goroutines and heap over its agent", func() {
		gopsDir := GinkgoT().TempDir()
		GinkgoT().Setenv("GOPS_CONFIG_DIR", gopsDir)
		child := tracingtest.StartGoChild(tracingtest.GoChildBinary(GinkgoT().TempDir()), "-gops", gopsDir)
		run, err := start(builtin("gops"), telltale.StartOptions{DataDir: GinkgoT().TempDir(), PID: child.PID, Duration: 500 * time.Millisecond})
		Expect(err).ToNot(HaveOccurred())
		result, err := run.Wait()
		Expect(err).ToNot(HaveOccurred())
		Expect(result.CheckpointErrors).To(BeEmpty())
		Expect(result.Captures).To(ConsistOf(
			And(HaveField("Kind", stacks.Kind), HaveField("Summary", HaveKeyWithValue("count", BeNumerically(">", 0)))),
			And(HaveField("Kind", goprof.Kind), HaveField("Summary", HaveKeyWithValue("count", BeNumerically(">", 0)))),
		))
	})
})

var _ = Describe("a definition's connections at start", func() {
	inline := header + `spec:
  connections:
    - name: orders
      type: postgres
      url: postgres://$(username):$(password)@db.example.test:5432/orders
      username: {valueFrom: {onePassword: op://dev/orders-db/username}}
`

	It("are the run's own layer, shadowing a stored one of the name for that run alone", func() {
		stored := storeWith(map[string]string{"orders": "sql_server"})
		applied, err := definition.Apply(decode(inline), telltale.StartOptions{Connections: stored})
		Expect(err).ToNot(HaveOccurred())
		Expect(applied.RunConnections).To(HaveLen(1))

		dataDir := GinkgoT().TempDir()
		host, err := traces.Open(traces.OpenOptions{DataDir: dataDir, RunID: "run-connections", Connections: stored, RunConnections: applied.RunConnections})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(host.Close(context.Background())).To(Succeed()) })

		layers := append(stored.Layers(), store.Layer{Name: traces.RunConnectionsDir, Dir: filepath.Join(host.Dir(), traces.RunConnectionsDir)})
		run, err := store.New(layers...)
		Expect(err).ToNot(HaveOccurred())
		orders, err := run.Find("connection://orders")
		Expect(err).ToNot(HaveOccurred())
		Expect(orders.Connection).To(And(HaveField("Type", "postgres"), HaveField("Username", "op://dev/orders-db/username")))
		standing, err := stored.Find("connection://orders")
		Expect(err).ToNot(HaveOccurred())
		Expect(standing.Connection.Type).To(Equal("sql_server"))
	})

	It("refuse a reference the connection store does not define, naming it", func() {
		_, err := definition.Apply(decode(header+"spec: {connections: [connection://missing]}"), telltale.StartOptions{Connections: storeWith(map[string]string{})})
		Expect(err).To(MatchError(ContainSubstring("the connection store defines no connection://missing")))
	})
})
