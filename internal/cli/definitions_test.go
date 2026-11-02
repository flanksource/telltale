package cli_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// probeDefinition is a host-only definition reading SELECT 1 from an
// in-memory sqlite database, named probe as its file must be.
const probeDefinition = `apiVersion: telltale.flanksource.com/v1alpha1
kind: TraceDefinition
metadata: {name: probe}
spec:
  collect:
    query:
      sql:
        - {type: sqlite, url: ":memory:", query: "SELECT 1 AS one"}
`

// definitionFile writes content as <dir>/probe.yaml.
func definitionFile(content string) string {
	path := filepath.Join(GinkgoT().TempDir(), "probe.yaml")
	Expect(os.WriteFile(path, []byte(content), 0o600)).To(Succeed())
	return path
}

var _ = Describe("telltale definitions", func() {
	It("runs a definition file as a host run and records it on the run", func() {
		dataDir := GinkgoT().TempDir()
		_, stderr, err := execute("run", "--definition", definitionFile(probeDefinition), "--data-dir", dataDir, "--duration", "500ms")
		Expect(err).ToNot(HaveOccurred(), stderr)
		result := manifest(dataDir)
		Expect(result.Captures).To(ConsistOf(HaveField("Kind", "query")))
		Expect(result.Definition).To(HaveValue(And(HaveField("Name", "probe"), HaveField("ResourceVersion", Not(BeEmpty())))))
	})

	It("validates a file, failing with each refused field's pointer", func() {
		stdout, _, err := execute("definitions", "validate", definitionFile(probeDefinition+"  every: soon\n"), "--format", "json")
		Expect(err).To(MatchError(ContainSubstring("1 error")))
		Expect(stdout).To(ContainSubstring("/spec/every"))
		Expect(stdout).To(ContainSubstring("probe-collect-query-sql"))
	})

	It("prints the CRD", func() {
		stdout, _, err := execute("definitions", "crd")
		Expect(err).ToNot(HaveOccurred())
		Expect(stdout).To(ContainSubstring("name: tracedefinitions.telltale.flanksource.com"))
	})
})
