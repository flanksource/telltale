package definition_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/flanksource/telltale/pkg/datadir"
	"github.com/flanksource/telltale/pkg/definition"
)

// scrape is a valid definition named name that scrapes endpoint.
func scrape(name, endpoint string) *definition.TraceDefinition {
	return decode(`apiVersion: telltale.flanksource.com/v1alpha1
kind: TraceDefinition
metadata: {name: ` + name + `}
spec: {collect: {prometheus: {scrape: [{endpoints: ['` + endpoint + `']}]}}}`)
}

var _ = Describe("the definition store", func() {
	var (
		cwd, global string
		defs        *definition.Store
	)

	BeforeEach(func() {
		cwd, global = GinkgoT().TempDir(), GinkgoT().TempDir()
		defs = definition.NewStore([]datadir.ConfigDir{{Name: datadir.ConfigCWD, Dir: cwd}, {Name: datadir.ConfigGlobal, Dir: global}})
	})

	put := func(layer string, def *definition.TraceDefinition, resourceVersion string) definition.Stored {
		stored, err := defs.Put(layer, def, resourceVersion, definition.Options{})
		Expect(err).ToNot(HaveOccurred())
		return stored
	}

	It("lists each name from its first layer, over the built-ins", func() {
		put(datadir.ConfigGlobal, scrape("checkout", "http://global:8080/metrics"), "")
		put(datadir.ConfigCWD, scrape("checkout", "http://cwd:8080/metrics"), "")
		put(datadir.ConfigGlobal, scrape("ps", "http://override:8080/metrics"), "")

		listed, err := defs.List()
		Expect(err).ToNot(HaveOccurred())
		Expect(listed).To(ConsistOf(
			And(HaveField("Name", "checkout"), HaveField("Layer", datadir.ConfigCWD),
				HaveField("Definition.Spec.Collect.Prometheus.Scrape", HaveExactElements(HaveField("Endpoints", ConsistOf("http://cwd:8080/metrics"))))),
			And(HaveField("Name", "ps"), HaveField("Layer", datadir.ConfigGlobal)),
			And(HaveField("Name", "gops"), HaveField("Layer", definition.LayerBuiltin)),
			And(HaveField("Name", "clicky"), HaveField("Layer", definition.LayerBuiltin)),
		))
	})

	It("writes <dir>/traces/<name>.yaml, read back at the version Put returned", func() {
		saved := put(datadir.ConfigGlobal, scrape("checkout", "http://localhost:8080/metrics"), "")
		Expect(saved.Path).To(Equal(filepath.Join(global, definition.Dir, "checkout.yaml")))
		got, err := defs.Get("checkout")
		Expect(err).ToNot(HaveOccurred())
		Expect(got.ResourceVersion).To(Equal(saved.ResourceVersion))
		Expect(got.Definition.ResourceVersion).To(Equal(saved.ResourceVersion))
	})

	It("replaces a definition at the version read, and refuses a stale one with a conflict", func() {
		first := put(datadir.ConfigGlobal, scrape("checkout", "http://a:8080/metrics"), "")
		second := put(datadir.ConfigGlobal, scrape("checkout", "http://b:8080/metrics"), first.ResourceVersion)
		Expect(second.ResourceVersion).ToNot(Equal(first.ResourceVersion))

		_, err := defs.Put(datadir.ConfigGlobal, scrape("checkout", "http://c:8080/metrics"), first.ResourceVersion, definition.Options{})
		var conflict *definition.ConflictError
		Expect(err).To(BeAssignableToTypeOf(conflict))
		Expect(err).To(MatchError(definition.ErrConflict))
		Expect(err).To(MatchError(ContainSubstring("is at resourceVersion " + second.ResourceVersion + ", not " + first.ResourceVersion)))
	})

	DescribeTable("refuses a write it cannot make",
		func(layer string, def func() *definition.TraceDefinition, message string) {
			_, err := defs.Put(layer, def(), "", definition.Options{})
			Expect(err).To(MatchError(ContainSubstring(message)))
		},
		Entry("to the built-ins", definition.LayerBuiltin, func() *definition.TraceDefinition { return scrape("ps", "http://a:8080/metrics") },
			"the builtin trace definitions are read-only"),
		Entry("an invalid definition", datadir.ConfigGlobal, func() *definition.TraceDefinition { return scrape("checkout", "ftp://a/metrics") },
			"/spec/collect/prometheus/scrape/0"),
		Entry("a create over an existing file", datadir.ConfigCWD, func() *definition.TraceDefinition {
			put(datadir.ConfigCWD, scrape("checkout", "http://a:8080/metrics"), "")
			return scrape("checkout", "http://b:8080/metrics")
		}, "already exists in cwd"),
	)

	It("deletes at the version read, and refuses a stale delete", func() {
		saved := put(datadir.ConfigCWD, scrape("checkout", "http://a:8080/metrics"), "")
		Expect(defs.Delete(datadir.ConfigCWD, "checkout", "stale")).To(MatchError(definition.ErrConflict))
		Expect(defs.Delete(datadir.ConfigCWD, "checkout", saved.ResourceVersion)).To(Succeed())
		_, err := defs.Get("checkout")
		Expect(err).To(MatchError(os.ErrNotExist))
	})

	It("refuses to delete a name that is no plain file name, before reading the layer", func() {
		nested := filepath.Join(cwd, definition.Dir, "nested")
		Expect(os.MkdirAll(nested, 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(nested, "checkout.yaml"), []byte("kind: TraceDefinition\n"), 0o644)).To(Succeed())
		Expect(defs.Delete(datadir.ConfigCWD, "nested/checkout", "")).To(MatchError(ContainSubstring("must be a plain file name")))
		Expect(defs.Delete(datadir.ConfigCWD, "check..out", "")).To(MatchError(ContainSubstring("must be a plain file name")))
		Expect(filepath.Join(nested, "checkout.yaml")).To(BeAnExistingFile())
	})

	It("reports a file whose metadata.name is not its file name, and refuses to get it", func() {
		Expect(os.MkdirAll(filepath.Join(cwd, definition.Dir), 0o755)).To(Succeed())
		data, err := definition.Encode(scrape("other", "http://a:8080/metrics"))
		Expect(err).ToNot(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(cwd, definition.Dir, "checkout.yaml"), data, 0o644)).To(Succeed())

		listed, err := defs.List()
		Expect(err).ToNot(HaveOccurred())
		Expect(listed).To(ContainElement(And(HaveField("Name", "checkout"), HaveField("Problem", `metadata.name "other" is not its file name "checkout"`))))
		_, err = defs.Get("checkout")
		Expect(err).To(MatchError(ContainSubstring(`metadata.name "other" is not its file name`)))
	})
})
