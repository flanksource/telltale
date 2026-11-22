package cli

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/flanksource/clicky/prompt"
	"github.com/flanksource/clicky/rpc"
	"github.com/flanksource/clicky/schemaform"
	"github.com/flanksource/telltale"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/spf13/cobra"
)

var _ = Describe("interactive run arguments", func() {
	It("installs -i and publishes the required connection chooser in the shared form schema", func() {
		root := NewRoot("test")
		Expect(root.PersistentFlags().Lookup("interactive")).NotTo(BeNil())
		Expect(root.PersistentFlags().Lookup("interactive").Shorthand).To(Equal("i"))
		cmd, _, err := root.Find([]string{"run", "sql_plancache"})
		Expect(err).NotTo(HaveOccurred())
		op, err := rpc.NewConverter(nil).ConvertCommand(cmd)
		Expect(err).NotTo(HaveOccurred())
		Expect(op.Path).To(Equal("/api/v1/run/sql_plancache"))
		raw, err := rpc.OperationInputSchema(*op, rpc.OperationSchemaOptions{})
		Expect(err).NotTo(HaveOccurred())
		var schema map[string]any
		Expect(json.Unmarshal(raw, &schema)).To(Succeed())
		Expect(schema["required"]).To(ContainElement("connection"))
		properties := schema["properties"].(map[string]any)
		Expect(properties).NotTo(HaveKey("flags"))
		Expect(properties["connection"]).To(HaveKeyWithValue("x-clicky-lookup", HaveKeyWithValue("url", "/api/v1/connection?types=sql_server")))
	})
	DescribeTable("prompts only for missing required params before starting the run",
		func(kind string, flags []string, lookup string, answers map[string]any, expected string) {
			manager := prompt.NewManager(prompt.NewMemory(prompt.MemoryConfig{}))
			prompt.SetDefault(manager)
			DeferCleanup(func() { prompt.SetDefault(nil) })
			DeferCleanup(func(previous func(context.Context, telltale.StartOptions) (*telltale.Run, error)) {
				startKindRun = previous
			}, startKindRun)
			var started telltale.StartOptions
			startKindRun = func(_ context.Context, options telltale.StartOptions) (*telltale.Run, error) {
				started = options
				return nil, errNotStarted
			}
			root := NewRoot("test")
			root.SilenceErrors = true
			root.SetArgs(append([]string{"run", kind, "--data-dir", GinkgoT().TempDir(), "--duration", "1s"}, flags...))
			ctx, cancel := context.WithCancel(prompt.WithScope(context.Background(), prompt.Scope{Owner: "required-params"}))
			DeferCleanup(cancel)
			done := make(chan error, 1)
			go func() { done <- root.ExecuteContext(ctx) }()
			var pending []prompt.PromptSnapshot
			Eventually(func() []prompt.PromptSnapshot {
				pending = manager.List(prompt.Filter{Owner: "required-params", Kind: "form", State: "pending"})
				return pending
			}).Should(HaveLen(1))
			var schema rpc.OpenAPISchema
			Expect(json.Unmarshal(pending[0].Schema, &schema)).To(Succeed())
			Expect(schema.Required).To(Equal(slices.Sorted(maps.Keys(answers))))
			Expect(schema.Properties).To(HaveLen(len(answers)))
			if lookup != "" {
				Expect(string(pending[0].Schema)).To(ContainSubstring(lookup))
			}
			Expect(manager.Resolve(pending[0].ID, prompt.Answer{Values: answers})).To(Succeed())
			Eventually(done).Should(Receive(MatchError(errNotStarted)))
			Expect(started.Traces).To(HaveLen(1))
			Expect(string(started.Traces[0].Params)).To(MatchJSON(expected))
			Expect(started.Duration).To(Equal(time.Second))
		},
		Entry("SQL Server plan cache", "sql_plancache", []string{"--databases", "billing,orders"}, "/api/v1/connection?types=sql_server", map[string]any{"connection": "connection://tenant/sample-db"},
			`{"connection":"connection://tenant/sample-db","databases":["billing","orders"]}`),
		Entry("SQL top's database seam", "sql_top", []string{"--databases", "billing,orders"}, "/api/v1/connection?types=postgres,sql_server", map[string]any{"connection": "connection://tenant/sample-db"},
			`{"connection":"connection://tenant/sample-db","databases":["billing","orders"]}`),
		Entry("a new SQL Server Extended Events session", "sql_xevent", []string{}, "/api/v1/connection?types=sql_server",
			map[string]any{"connection": "connection://tenant/sample-db"},
			`{"connection":"connection://tenant/sample-db"}`),
		Entry("an existing SQL Server Extended Events session", "sql_xevent", []string{"--session", "system_health"}, "/api/v1/connection?types=sql_server",
			map[string]any{"connection": "connection://tenant/sample-db"},
			`{"connection":"connection://tenant/sample-db","session":"system_health"}`),
		Entry("a nested connection", "spans", []string{"--seam", "host", "--opensearch-index", "example-spans"}, "/api/v1/connection", map[string]any{"opensearch-connection": "connection://tenant/sample-spans"},
			`{"target":{"seam":"host"},"opensearch":{"index":"example-spans","connection":"connection://tenant/sample-spans"}}`),
		Entry("JVM trace method targets", "jvm_trace", []string{"--pid", "1234", "--exceptions-only"}, "",
			map[string]any{"targets": []any{map[string]any{"class": "example.Worker", "method": "compute"}}}, `{"targets":[{"class":"example.Worker","method":"compute"}],"exceptionsOnly":true}`),
	)
	It("skips the connection prompt for SQL top attached to an agent PID", func() {
		manager := prompt.NewManager(prompt.NewMemory(prompt.MemoryConfig{}))
		prompt.SetDefault(manager)
		DeferCleanup(func() { prompt.SetDefault(nil) })
		ctx := prompt.WithScope(context.Background(), prompt.Scope{Owner: "sql-top-agent"})
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		root := NewRoot("test")
		root.SilenceErrors = true
		root.SetArgs([]string{"run", "sql_top", "--data-dir", GinkgoT().TempDir(), "--seam", "agent", "--pid", "1234"})
		cmd, _, err := root.Find([]string{"run", "sql_top"})
		Expect(err).NotTo(HaveOccurred())
		cmd.RunE = func(_ *cobra.Command, _ []string) error { return errNotStarted }
		Expect(root.ExecuteContext(ctx)).To(MatchError(errNotStarted))
		Expect(manager.List(prompt.Filter{Owner: "sql-top-agent", Kind: "form"})).To(BeEmpty())
	})
	It("returns missing required flags to HTTP callers without prompting", func() {
		manager := prompt.NewManager(prompt.NewMemory(prompt.MemoryConfig{}))
		prompt.SetDefault(manager)
		DeferCleanup(func() { prompt.SetDefault(nil) })
		root := NewRoot("test")
		root.SilenceErrors = true
		root.SetArgs([]string{"run", "sql_plancache", "--data-dir", GinkgoT().TempDir()})
		ctx := prompt.WithScope(context.Background(), prompt.Scope{Owner: "remote-run"})
		ctx, cancel := context.WithTimeout(rpc.ContextWithRequest(ctx, &http.Request{}), time.Second)
		defer cancel()
		Expect(root.ExecuteContext(ctx)).To(MatchError(ContainSubstring(`required flag(s) "connection" not set`)))
		Expect(manager.List(prompt.Filter{Owner: "remote-run", Kind: "form"})).To(BeEmpty())
	})
	It("fills the schema before running and preserves supplied command arguments and flags", func() {
		manager := prompt.NewManager(prompt.NewMemory(prompt.MemoryConfig{}))
		prompt.SetDefault(manager)
		DeferCleanup(func() { prompt.SetDefault(nil) })
		root := NewRoot("test")
		cmd, _, err := root.Find([]string{"run", "sql_top"})
		Expect(err).NotTo(HaveOccurred())
		root.SetArgs([]string{"run", "sql_top", "-i", "--connection", "connection://saved", "--duration", "1s", "--", "echo", "captured"})
		ctx, cancel := context.WithCancel(prompt.WithScope(context.Background(), prompt.Scope{Owner: "telltale-form"}))
		DeferCleanup(cancel)
		root.SilenceErrors = true
		done := make(chan error, 1)
		go func() { done <- root.ExecuteContext(ctx) }()
		var snapshot prompt.PromptSnapshot
		Eventually(func() int {
			pending := manager.List(prompt.Filter{Owner: "telltale-form", Kind: "form", State: "pending"})
			if len(pending) > 0 {
				snapshot = pending[0]
			}
			return len(pending)
		}).Should(Equal(1))
		Expect(snapshot.Value).To(HaveKeyWithValue("connection", "connection://saved"))
		Expect(snapshot.Value).To(HaveKeyWithValue("args", Equal([]any{"echo", "captured"})))
		Expect(manager.Resolve(snapshot.ID, prompt.Answer{Cancelled: true})).To(Succeed())
		Eventually(done).Should(Receive(MatchError(ContainSubstring("prompt cancelled"))))
		Expect(cmd.Flags().Changed("connection")).To(BeTrue())
	})
	It("filters the same catalog for terminal lookup and shell completion", func() {
		dir := GinkgoT().TempDir()
		GinkgoT().Setenv("XDG_CONFIG_HOME", dir)
		GinkgoT().Chdir(dir)
		folder := filepath.Join(dir, ".connections")
		Expect(os.MkdirAll(folder, 0700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(folder, "database.yaml"), []byte("apiVersion: mission-control.flanksource.com/v1\nkind: Connection\nmetadata: {name: sample-db, namespace: tenant}\nspec: {postgres: {url: {value: 'postgres://localhost/example'}}}\n"), 0600)).To(Succeed())
		root := NewRoot("test")
		for _, kind := range []string{"sql_top", "sql_plancache"} {
			cmd, _, err := root.Find([]string{"run", kind})
			Expect(err).NotTo(HaveOccurred())
			complete, ok := cmd.GetFlagCompletionFunc("connection")
			Expect(ok).To(BeTrue())
			Expect(complete).NotTo(BeNil())
			choices, _ := complete(cmd, nil, "")
			if kind == "sql_top" {
				Expect(choices).To(ConsistOf("connection://tenant/sample-db\tsample-db (postgres)"))
			} else {
				Expect(choices).To(BeEmpty())
			}
			_, directive := complete(cmd, nil, "connection://does-not-exist")
			Expect(directive & cobra.ShellCompDirectiveNoFileComp).NotTo(BeZero())
		}
		result, err := lookupRunConnections(context.Background(), schemaform.LookupRequest{Spec: schemaform.LookupSpec{Filter: "connection", URL: "/api/v1/connection?types=postgres,sql_server"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Options).To(ConsistOf(schemaform.Option{
			Value: "connection://tenant/sample-db", Raw: "connection://tenant/sample-db", Label: "tenant/sample-db (postgres)", Description: "connection://tenant/sample-db",
		}))
		_, err = lookupRunConnections(context.Background(), schemaform.LookupRequest{Spec: schemaform.LookupSpec{Filter: "connection", URL: "/api/v1/connection?types=sql_server"}})
		Expect(err).To(MatchError(ContainSubstring("no configured connections match")))
	})
})
