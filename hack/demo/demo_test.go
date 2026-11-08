package main

import (
	"encoding/json"
	"testing"

	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/telltale/pkg/traces"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestDemo(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Real demo trace") }

var _ = Describe("capture coverage", func() {
	It("rejects missing kinds and empty captures", func() {
		_, err := validateCoverage([]string{"exec", "terminal"}, map[string][]query.Row{"exec": {{"exitCode": 0, "name": "git"}}})
		Expect(err).To(MatchError(ContainSubstring("terminal")))
	})
	It("requires meaningful records instead of counting failed operations", func() {
		_, err := validateCoverage([]string{"prometheus"}, map[string][]query.Row{"prometheus": {{"health": "down", "samples": 0, "error": "connection refused"}}})
		Expect(err).To(MatchError(ContainSubstring("prometheus")))
	})
	It("accepts genuine successful captures and reports their complete coverage", func() {
		result, err := validateCoverage([]string{"exec", "terminal"}, map[string][]query.Row{
			"exec":     {{"exitCode": 0, "name": "git"}},
			"terminal": {{"kind": "o", "data": "git version"}, {"kind": "exit", "exitCode": 0}},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal([]evidence{{Kind: "exec", Count: 1, Meaningful: true}, {Kind: "terminal", Count: 2, Meaningful: true}}))
	})
	It("rejects a terminal without a measured exit status", func() {
		_, err := validateCoverage([]string{"terminal"}, map[string][]query.Row{"terminal": {{"kind": "o", "data": "git version"}, {"kind": "exit"}}})
		Expect(err).To(HaveOccurred())
	})
	It("rejects a capture with dropped records", func() {
		_, err := validateCoverage([]string{"exec"}, map[string][]query.Row{"exec": {{"name": "git", "exitCode": 0, "dropped": 1}}})
		Expect(err).To(HaveOccurred())
	})
	It("recognizes native JFR stacks and windowed Prometheus query rows", func() {
		_, err := validateCoverage([]string{"jvm_jfr", "query"}, map[string][]query.Row{
			"jvm_jfr": {{"stack": "DemoWorkload.compute", "value": 2}},
			"query":   {{"provider": "prometheus", "row": map[string]any{"__name__": "demo_requests_total", "value": 3}}},
		})
		Expect(err).NotTo(HaveOccurred())
	})
	It("validates parameters for every registered kind before starting workloads", func() {
		specs, err := demoSpecs("http://127.0.0.1:43210", "telltale_demo_test", 12345)
		Expect(err).NotTo(HaveOccurred())
		kinds, err := traces.Kinds()
		Expect(err).NotTo(HaveOccurred())
		var names []string
		for _, spec := range specs {
			names = append(names, spec.Kind)
			Expect(traces.Validate(kinds, spec.Kind, spec.Params)).To(Succeed(), spec.Kind)
		}
		Expect(names).To(Equal(traces.KindNames(kinds)))
	})
	It("rejects loss reported inside a completed session summary", func() {
		Expect(validateSessions([]sessionEvidence{{Kind: "exec", State: query.SessionCompleted, Summary: json.RawMessage(`{"source":{"dropped":1}}`)}})).To(MatchError(ContainSubstring("dropped")))
	})
	It("requires a single LLM request with measured response usage", func() {
		_, err := validateCoverage([]string{"llm"}, map[string][]query.Row{"llm": {{"phase": "request"}, {"phase": "response", "response": "example reply"}}})
		Expect(err).To(HaveOccurred())
	})
})
