package definition_test

import (
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/flanksource/telltale/pkg/definition"
)

func TestDefinition(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Trace definitions")
}

// load decodes the definition file at path, relative to this package.
func load(path string) *definition.TraceDefinition {
	data, err := os.ReadFile(path)
	Expect(err).ToNot(HaveOccurred())
	def, err := definition.Decode(data)
	Expect(err).ToNot(HaveOccurred(), path)
	return def
}

// decode decodes a definition given inline.
func decode(yaml string) *definition.TraceDefinition {
	def, err := definition.Decode([]byte(yaml))
	Expect(err).ToNot(HaveOccurred())
	return def
}

// examples are the definition files the specs hold to the schema and the
// static validation: the shipped examples and the every-kind fixture.
var examples = []string{
	filepath.Join("..", "..", "examples", "definitions", "checkout.yaml"),
	filepath.Join("..", "..", "examples", "definitions", "logs-and-queries.yaml"),
	filepath.Join("testdata", "all-kinds.yaml"),
}
