package groups_test

import (
	"os"
	"path/filepath"

	"github.com/flanksource/telltale/pkg/tracing/groups"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func writeCatalog(content string) string {
	path := filepath.Join(GinkgoT().TempDir(), "groups.yaml")
	Expect(os.WriteFile(path, []byte(content), 0o600)).To(Succeed())
	return path
}

func ruleNames(catalog groups.Catalog) []string {
	names := make([]string, 0, len(catalog.Rules))
	for _, rule := range catalog.Rules {
		names = append(names, rule.Name)
	}
	return names
}

var _ = Describe("LoadCatalog", func() {
	It("puts a file's new rules ahead of the built-ins, replaces one by name in place and drops a disabled one", func() {
		builtin, err := groups.LoadCatalog()
		Expect(err).NotTo(HaveOccurred())

		catalog, err := groups.LoadCatalog(writeCatalog(`
groups:
  - name: acme-server
    kind: service
    match:
      - exec: [acme-server]
  - name: gavel
    kind: build
    match:
      - exec: [gavel-dev]
  - name: gopls
    disabled: true
`))
		Expect(err).NotTo(HaveOccurred())

		expected := []string{"acme-server"}
		for _, name := range ruleNames(builtin) {
			if name != "gopls" {
				expected = append(expected, name)
			}
		}
		Expect(ruleNames(catalog)).To(Equal(expected))
		gavel, ok := catalog.Rule("gavel")
		Expect(ok).To(BeTrue())
		Expect(gavel.Kind).To(Equal(groups.KindBuild))
	})

	DescribeTable("refuses a catalog it cannot apply, naming the fault",
		func(content, fault string) {
			_, err := groups.LoadCatalog(writeCatalog(content))
			Expect(err).To(MatchError(ContainSubstring(fault)))
		},
		Entry("an unknown field", "groups:\n  - name: x\n    kind: tool\n    matches: []\n", "field matches not found"),
		Entry("a rule without a match", "groups:\n  - name: x\n    kind: tool\n", `"x": no match`),
		Entry("an unknown kind", "groups:\n  - name: x\n    kind: daemon\n    match: [{exec: [x]}]\n", `kind "daemon"`),
		Entry("a bad exec glob", "groups:\n  - name: x\n    kind: tool\n    match: [{exec: ['[']}]\n", "syntax error in pattern"),
		Entry("an unknown agent source", "groups:\n  - name: x\n    kind: agent\n    match: [{source: gemini}]\n", `source "gemini"`),
		Entry("an unknown target", "groups:\n  - name: x\n    kind: tool\n    target: argv\n    match: [{exec: [x]}]\n", `target "argv"`),
		Entry("a rule named twice", "groups:\n  - name: x\n    kind: tool\n    match: [{exec: [x]}]\n  - name: x\n    kind: tool\n    match: [{exec: [y]}]\n", `"x" twice`),
		Entry("disabling a rule that is not built in", "groups:\n  - name: nope\n    disabled: true\n", `disables "nope"`),
	)

	It("refuses a catalog file that does not exist", func() {
		_, err := groups.LoadCatalog(filepath.Join(GinkgoT().TempDir(), "missing.yaml"))
		Expect(err).To(MatchError(ContainSubstring("missing.yaml")))
	})
})
