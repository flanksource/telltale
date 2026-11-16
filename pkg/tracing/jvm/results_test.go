package jvm_test

import (
	"encoding/json"

	"github.com/flanksource/clicky/api"
	catalog "github.com/flanksource/telltale/internal/jvm"
	"github.com/flanksource/telltale/pkg/tracing/jvm"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("operation results", func() {
	It("decodes an operation with a typed model into it, and refuses a malformed one", func() {
		result, err := jvm.Result(jvm.HeapHistogramOp, nil, json.RawMessage(agentHistogram))
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(BeAssignableToTypeOf(jvm.HeapHistogram{}))
		_, err = jvm.Result(jvm.HeapHistogramOp, nil, json.RawMessage(`{}`))
		Expect(err).To(HaveOccurred())
	})

	It("models every built-in operation in the catalog, refusing an empty object instead of printing it raw", func() {
		var raw []string
		for _, operation := range catalog.Operations() {
			if result, err := jvm.Result(operation.Name, nil, json.RawMessage(`{}`)); err == nil {
				if _, isRaw := result.(jvm.RawResult); isRaw {
					raw = append(raw, operation.Name)
				}
			}
		}
		Expect(catalog.Operations()).To(HaveLen(34))
		Expect(raw).To(BeEmpty())
	})

	It("names what a write changed from its request", func() {
		result, err := jvm.Result("jvm.logging.set", json.RawMessage(`{"name":"global","level":"FINE"}`), json.RawMessage(`{"applied":true}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(result.(interface{ Pretty() api.Text }).Pretty().String()).To(Equal("✓ logger global → FINE"))
	})

	It("keeps an operation without a model as the agent sent it, indented for the terminal", func() {
		raw := json.RawMessage(`{"id":"ext-1","operations":["acme.ping"]}`)
		result, err := jvm.Result("acme.extension.ping", nil, raw)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(jvm.RawResult(raw)))
		encoded, err := json.Marshal(result)
		Expect(err).NotTo(HaveOccurred())
		Expect(encoded).To(MatchJSON(raw))
		Expect(result.(jvm.RawResult).Pretty().String()).To(Equal("{\n  \"id\": \"ext-1\",\n  \"operations\": [\n    \"acme.ping\"\n  ]\n}"))
	})

	It("reads an empty result as no result", func() {
		result, err := jvm.Result("acme.extension.reset", nil, nil)
		Expect(err).NotTo(HaveOccurred())
		encoded, err := json.Marshal(result)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(encoded)).To(Equal("null"))
		Expect(result.(jvm.RawResult).Pretty().String()).To(Equal("✓ done · no result"))
	})

	It("refuses a result that is not JSON", func() {
		_, err := jvm.Result("acme.extension.ping", nil, json.RawMessage(`{"id":`))
		Expect(err).To(MatchError(ContainSubstring("acme.extension.ping")))
	})
})
