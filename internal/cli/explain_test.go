package cli_test

import (
	"bytes"
	"encoding/json"

	"github.com/flanksource/telltale/internal/cli"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("explain", func() {
	It("lists registered kinds and rejects excess arguments", func() {
		root := cli.NewRoot("test")
		out := &bytes.Buffer{}
		root.SetOut(out)
		root.SetArgs([]string{"explain", "--json"})
		Expect(root.Execute()).To(Succeed())
		var kinds []map[string]any
		Expect(json.Unmarshal(out.Bytes(), &kinds)).To(Succeed())
		Expect(kinds).To(ContainElement(HaveKeyWithValue("name", "http")))
		root = cli.NewRoot("test")
		root.SetArgs([]string{"explain", "http", "sql"})
		Expect(root.Execute()).To(HaveOccurred())
	})
	It("returns the HTTP parameter schema and record columns as JSON", func() {
		root := cli.NewRoot("test")
		out := &bytes.Buffer{}
		root.SetOut(out)
		root.SetArgs([]string{"explain", "http", "--json"})
		Expect(root.Execute()).To(Succeed())
		var result map[string]any
		Expect(json.Unmarshal(out.Bytes(), &result)).To(Succeed())
		Expect(result).To(HaveKey("params"))
		Expect(result).To(HaveKey("columns"))
		Expect(result["name"]).To(Equal("http"))
		params := result["params"].(map[string]any)["properties"].(map[string]any)
		Expect(params["mode"].(map[string]any)["enum"]).To(Equal([]any{"connect", "mitm"}))
	})
	It("rejects unknown kinds", func() {
		root := cli.NewRoot("test")
		root.SetArgs([]string{"explain", "missing"})
		Expect(root.Execute()).To(MatchError(ContainSubstring("unknown trace kind")))
	})
	It("completes registered kind names", func() {
		root := cli.NewRoot("test")
		cmd, _, err := root.Find([]string{"explain"})
		Expect(err).NotTo(HaveOccurred())
		names, _ := cmd.ValidArgsFunction(cmd, nil, "htt")
		Expect(names).To(ContainElement("http"))
	})
})
