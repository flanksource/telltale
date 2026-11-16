package cli

import (
	"context"
	"encoding/json"
	"github.com/flanksource/clicky/prompt"
	"github.com/flanksource/clicky/rpc"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/spf13/cobra"
	"os"
	"path/filepath"
)

var _ = Describe("interactive run arguments", func() {
	It("installs -i and publishes the required connection chooser in the shared form schema", func() {
		root := NewRoot("test")
		Expect(root.PersistentFlags().Lookup("interactive")).NotTo(BeNil())
		Expect(root.PersistentFlags().Lookup("interactive").Shorthand).To(Equal("i"))
		cmd, _, err := root.Find([]string{"run", "sql_top"})
		Expect(err).NotTo(HaveOccurred())
		op, err := rpc.NewConverter(nil).ConvertCommand(cmd)
		Expect(err).NotTo(HaveOccurred())
		Expect(op.Path).To(Equal("/api/v1/run/sql_top"))
		raw, err := rpc.OperationInputSchema(*op, rpc.OperationSchemaOptions{})
		Expect(err).NotTo(HaveOccurred())
		var schema map[string]any
		Expect(json.Unmarshal(raw, &schema)).To(Succeed())
		Expect(schema["required"]).To(ContainElement("connection"))
		properties := schema["properties"].(map[string]any)
		Expect(properties).NotTo(HaveKey("flags"))
		Expect(properties["connection"]).To(HaveKeyWithValue("x-clicky-lookup", HaveKeyWithValue("url", "/api/v1/connection?types=postgres,sql_server")))
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
	It("registers a connection completion for each kind that accepts a connection", func() {
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
	})
})
