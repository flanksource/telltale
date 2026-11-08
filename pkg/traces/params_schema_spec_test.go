package traces_test

import (
	"encoding/json"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/flanksource/telltale/pkg/traces"
)

// schemaGoldens are each kind's params schema as it was reflected before the
// kinds split their params into Options and a target; added are the
// properties each kind gained since, which may differ from the golden.
var schemaGoldens = map[string][]string{
	"exec": nil, "goprof": nil, "jvm_jfr": nil, "llm": nil,
	"prometheus": nil, "spans": nil, "sql_deadlock": nil, "sql_plancache": nil,
	"sql_xevent": nil, "stacks": nil, "terminal": nil,
	"http": {"listen", "export"},
	"sql":  {"listen", "export"},
	"logs": {"target"},
	// query's golden is commons-db's own query kind's schema: telltale's
	// takes the same params, plus the host target every telltale kind has,
	// and its provider, the same type, is marked one JSON value for the CLI.
	"query": {"target", "provider"},
}

// schemaProperties reads a params schema as its JSON properties, so two
// reflections compare by what a client receives.
func schemaProperties(data []byte) map[string]json.RawMessage {
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	Expect(json.Unmarshal(data, &schema)).To(Succeed())
	return schema.Properties
}

var _ = Describe("a kind's params schema", func() {
	kinds, err := traces.Kinds()

	for kind, added := range schemaGoldens {
		It("keeps the "+kind+" properties a client already sends", func() {
			Expect(err).NotTo(HaveOccurred())
			golden, readErr := os.ReadFile(filepath.Join("testdata", "params", kind+".json"))
			Expect(readErr).NotTo(HaveOccurred())
			plugin, lookupErr := traces.Lookup(kinds, kind)
			Expect(lookupErr).NotTo(HaveOccurred())
			schema, schemaErr := plugin.Params()
			Expect(schemaErr).NotTo(HaveOccurred())
			reflected, marshalErr := json.Marshal(schema)
			Expect(marshalErr).NotTo(HaveOccurred())

			if len(added) == 0 {
				Expect(reflected).To(MatchJSON(golden), kind)
				return
			}
			want, got := schemaProperties(golden), schemaProperties(reflected)
			for _, name := range added {
				delete(want, name)
				Expect(got).To(HaveKey(name), "%s gained %s", kind, name)
				delete(got, name)
			}
			Expect(len(got)).To(Equal(len(want)), "%s properties", kind)
			for name, property := range want {
				Expect(got).To(HaveKey(name))
				Expect(got[name]).To(MatchJSON(property), "%s.%s", kind, name)
			}
		})
	}
})
