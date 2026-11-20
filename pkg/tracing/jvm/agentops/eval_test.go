package agentops_test

import (
	"encoding/json"

	"github.com/flanksource/telltale/pkg/tracing/jvm/agentops"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const agentGroovyMap = `{"graph":{"id":null,"label":"result","kind":"map","path":null,"expandable":false,"type":"java.util.LinkedHashMap","value":null,"raw":null,"children":[` +
	`{"id":null,"label":"name","kind":"scalar","path":null,"expandable":false,"type":"java.lang.String","value":"idle","raw":null,"children":null},` +
	`{"id":null,"label":"sizes [2]","kind":"list","path":null,"expandable":false,"type":"java.util.ArrayList","value":null,"raw":null,"children":[` +
	`{"id":null,"label":"0","kind":"scalar","path":null,"expandable":false,"type":"java.lang.Integer","value":"1","raw":null,"children":null},` +
	`{"id":null,"label":"1","kind":"scalar","path":null,"expandable":false,"type":"java.lang.Integer","value":"2","raw":null,"children":null}]},` +
	`{"id":null,"label":"nested","kind":"map","path":null,"expandable":false,"type":"java.util.LinkedHashMap","value":null,"raw":null,"children":[` +
	`{"id":null,"label":"a","kind":"scalar","path":null,"expandable":false,"type":null,"value":"null","raw":null,"children":null},` +
	`{"id":null,"label":"b","kind":"scalar","path":null,"expandable":false,"type":"java.lang.Boolean","value":"true","raw":null,"children":null}]},` +
	`{"id":null,"label":"when","kind":"scalar","path":null,"expandable":false,"type":"java.util.Date","value":"Thu Jan 01 02:00:00 IST 1970","raw":null,"children":null}]},` +
	`"stdout":"","stderr":"","elapsedMs":173}`

const agentOgnlThread = `{"graph":{"id":null,"label":"result","kind":"bean","path":null,"expandable":false,"type":"java.lang.Thread","value":null,"raw":null,"children":[` +
	`{"id":null,"label":"name","kind":"error","path":null,"expandable":false,"type":"java.lang.String","value":null,"raw":"unreadable: java.lang.reflect.InaccessibleObjectException","children":null},` +
	`{"id":null,"label":"tid","kind":"error","path":null,"expandable":false,"type":"long","value":null,"raw":"unreadable: java.lang.reflect.InaccessibleObjectException","children":null}]}}`

var _ = Describe("groovy and ognl evaluation", func() {
	It("prints a scalar groovy result on one line with the script's output below it", func() {
		result, err := agentops.DecodeGroovy(json.RawMessage(`{"value":3,"stdout":"hi\n","stderr":"","elapsedMs":211}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(agentops.GroovyResult{Evaluation: agentops.Evaluation{Value: json.RawMessage(`3`)}, Stdout: "hi\n", ElapsedMs: 211}))
		Expect(plain(result)).To(Equal([]string{"= 3 · 211ms", "hi"}))
	})

	It("keeps a null groovy result and its stderr", func() {
		result, err := agentops.DecodeGroovy(json.RawMessage(`{"value":null,"stdout":"","stderr":"careful\n","elapsedMs":168}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(plain(result)).To(Equal([]string{"= null · 168ms", "careful"}))
	})

	It("draws a groovy object graph as an indented tree", func() {
		result, err := agentops.DecodeGroovy(json.RawMessage(agentGroovyMap))
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Graph.Children[1].Children[0]).To(Equal(agentops.Node{Label: "0", Kind: "scalar", Type: "java.lang.Integer", Value: "1"}))
		Expect(plain(result)).To(Equal([]string{
			"= LinkedHashMap · 173ms",
			"├─ name: String = \"idle\"",
			"├─ sizes [2]: ArrayList",
			"│  ├─ 0: Integer = 1",
			"│  └─ 1: Integer = 2",
			"├─ nested: LinkedHashMap",
			"│  ├─ a = null",
			"│  └─ b: Boolean = true",
			"└─ when: Date = Thu Jan 01 02:00:00 IST 1970",
		}))
	})

	It("prints a scalar ognl result on one line", func() {
		result, err := agentops.DecodeOgnl(json.RawMessage(`{"value":"idle"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(plain(result)).To(Equal([]string{`= "idle"`}))
	})

	It("marks the unreadable members of an ognl graph", func() {
		result, err := agentops.DecodeOgnl(json.RawMessage(agentOgnlThread))
		Expect(err).NotTo(HaveOccurred())
		Expect(plain(result)).To(Equal([]string{
			"= Thread",
			"├─ name: String ✗ unreadable: InaccessibleObjectException",
			"└─ tid: long ✗ unreadable: InaccessibleObjectException",
		}))
	})

	It("refuses an evaluation that carries neither a value nor a graph", func() {
		for _, raw := range []string{`{"stdout":"","stderr":"","elapsedMs":1}`, `[]`} {
			_, err := agentops.DecodeGroovy(json.RawMessage(raw))
			Expect(err).To(HaveOccurred(), raw)
		}
		_, err := agentops.DecodeGroovy(json.RawMessage(`{"value":1}`))
		Expect(err).To(HaveOccurred(), "a groovy result always reports its elapsed time")
	})
})
