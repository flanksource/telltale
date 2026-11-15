package jvm

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestJVM(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "JVM operations") }

var _ = Describe("ReadParamFile", func() {
	write := func(data []byte) string {
		path := filepath.Join(GinkgoT().TempDir(), "param")
		Expect(os.WriteFile(path, data, 0o600)).To(Succeed())
		return path
	}

	It("reads text as is and binary content as base64", func() {
		jar := []byte{0x50, 0x4b, 0x03, 0x04, 0x00, 0xff}
		Expect(ReadParamFile(write([]byte("return 3 + 4")), false)).To(Equal("return 3 + 4"))
		Expect(ReadParamFile(write(jar), true)).To(Equal(base64.StdEncoding.EncodeToString(jar)))
	})

	It("refuses a file over the 16 MiB upload limit", func() {
		_, err := ReadParamFile(write(make([]byte, paramFileLimit+1)), true)
		Expect(err).To(MatchError(ContainSubstring("exceeds 16 MiB")))
	})
})

var _ = Describe("embedded operation catalog", func() {
	It("decodes every built-in operation with an object params schema", func() {
		names := make([]string, 0, len(Operations()))
		for _, operation := range Operations() {
			names = append(names, operation.Name)
			Expect(operation.Params.Type).To(Equal("object"), operation.Name)
			Expect(operation.Description).NotTo(BeEmpty(), operation.Name)
		}
		Expect(names).To(ContainElements("jvm.jmx.search", "jvm.classes", "jvm.classes.source", "jvm.groovy.eval", "jvm.extensions.install", "jvm.logging.set"))
		Expect(slices.Compact(slices.Sorted(slices.Values(names)))).To(HaveLen(len(names)))
	})

	It("places every operation at its declared command", func() {
		commands := map[string]string{}
		for _, operation := range Operations() {
			commands[operation.Name] = strings.Join(operation.Command, " ")
		}
		Expect(commands).To(Equal(map[string]string{
			"jvm.extensions.list": "jvm extensions list", "jvm.extensions.install": "jvm extensions install",
			"jvm.extensions.replace": "jvm extensions replace", "jvm.extensions.unload": "jvm extensions unload",
			"jvm.catalog": "jvm catalog", "jvm.status": "jvm status",
			"jvm.jmx.search": "jvm jmx search", "jvm.jmx.info": "jvm jmx info", "jvm.jmx.read": "jvm jmx read",
			"jvm.jmx.write": "jvm jmx write", "jvm.jmx.invoke": "jvm jmx invoke", "jvm.metrics": "jvm metrics",
			"jvm.threads": "jvm threads", "jvm.threads.dump": "jvm threads dump", "jvm.threads.stack": "jvm threads stack", "jvm.heap.histogram": "jvm heap histogram",
			"jvm.logging.list": "jvm logging list", "jvm.logging.set": "jvm logging set",
			"jvm.classes": "jvm classes", "jvm.loaders": "jvm loaders",
			"jvm.groovy.eval": "jvm groovy eval", "jvm.ognl.eval": "jvm ognl eval",
			"jvm.classes.source": "jvm decompile", "jvm.classes.bytecode": "jvm decompile bytecode",
			"jvm.classes.compile": "jvm patch compile", "jvm.classes.patch": "jvm patch apply",
			"jvm.classes.restore": "jvm patch restore", "jvm.classes.lease": "jvm patch lease",
			"jvm.classes.patches": "jvm patch list", "jvm.classes.instrument": "jvm trace instrument",
			"jvm.probes.list": "jvm trace probes", "jvm.probes.read": "jvm trace read", "jvm.probes.freeze": "jvm trace freeze",
			"jvm.notifications.list": "jvm notifications list",
		}))
	})

	It("refuses an operation without a description, a command or a closed params object", func() {
		for _, data := range []string{
			`[{"name":"jvm.x","command":"jvm x","paramsSchema":{"type":"object"}}]`,
			`[{"name":"jvm.x","description":"d","paramsSchema":{"type":"object"}}]`,
			`[{"name":"jvm.x","description":"d","command":"x","paramsSchema":{"type":"object"}}]`,
			`[{"name":"jvm.x","description":"d","command":"jvm x","paramsSchema":{"type":"object","additionalProperties":true}}]`,
			`[{"name":"jvm.x","description":"d","command":"jvm x","paramsSchema":{"type":"string"}}]`,
		} {
			_, err := decodeCatalog([]byte(data))
			Expect(err).To(HaveOccurred(), data)
		}
	})
})
