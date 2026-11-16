package definition_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/flanksource/telltale/pkg/definition"
)

var _ = Describe("a compiled definition", func() {
	// Each entry compiles to the TraceSpec the free-form trace list wrote for
	// the same capture: its kind, params with the implied seam, and
	// checkpoint params. A postgres proxy alone differs: it exports the
	// proxied DSN under its env only, so several can share a run.
	DescribeTable("arms the captures the free-form specs armed",
		func(entry, kind, params, checkpoint string) {
			specs, err := definition.Specs(decode(header+"spec:\n"+entry), definition.Options{})
			Expect(err).ToNot(HaveOccurred())
			Expect(specs).To(HaveLen(1))
			Expect(specs[0].Kind).To(Equal(kind))
			Expect(specs[0].Params).To(MatchJSON(params))
			if checkpoint == "" {
				Expect(specs[0].Checkpoint).To(BeNil())
			} else {
				Expect(specs[0].Checkpoint).To(MatchJSON(checkpoint))
			}
		},
		Entry("an http proxy", "  proxy: {http: [{mode: mitm, hosts: [api.example.com], bodies: 64KiB}]}",
			"http", `{"target":{"seam":"proxy"},"mode":"mitm","hosts":["api.example.com"],"bodies":"64KiB"}`, ""),
		Entry("a postgres proxy", "  proxy: {postgres: [{env: DATABASE_URL, connection: orders, maxStatements: 20000}]}",
			"sql", `{"target":{"seam":"proxy"},"env":"DATABASE_URL","connection":"orders","maxStatements":20000,"export":["DATABASE_URL"]}`, ""),
		Entry("gops goroutine dumps", "  collect: {go: {gops: [{}]}}",
			"stacks", `{"target":{"seam":"process"}}`, `{"transport":"gops"}`),
		Entry("a pprof cpu profile", "  collect: {go: {pprof: [{source: pprof, profile: cpu, seconds: 10}]}}",
			"goprof", `{"target":{"seam":"process"}}`, `{"source":"pprof","profile":"cpu","seconds":10}`),
		Entry("a mutex profile, allowed to set the runtime's rate", "  instrument: {go: {pprof: [{profile: mutex}]}}",
			"goprof", `{"target":{"seam":"process","allowMutation":true}}`, `{"source":"gops","profile":"mutex"}`),
		Entry("a JFR recording through a Java agent",
			"  instrument: {jvm: {flightRecorder: [{agent: {selector: {service.name: pricing}, wait: true}, settings: profile, threads: true, maxStackDepth: 64}]}}",
			"jvm_jfr", `{"target":{"seam":"agent","selector":{"service.name":"pricing"},"wait":true,"allowMutation":true},"engine":"jfr","settings":"profile","threads":true,"maxStackDepth":64}`, ""),
		Entry("native thread stacks", "  collect: {process: {nativeStacks: [{}]}}",
			"stacks", `{"target":{"seam":"process"}}`, `{"transport":"native"}`),
		Entry("process resources", "  collect: {process: {resources: [{interval: 1s, facets: [sockets, connections]}]}}",
			"process", `{"target":{"seam":"process"},"interval":"1s","facets":["sockets","connections"]}`, ""),
		Entry("a Prometheus scrape", "  collect: {prometheus: {scrape: [{endpoints: ['http://localhost:8080/metrics'], interval: 15s, labels: {env: dev}}]}}",
			"prometheus", `{"target":{"seam":"host"},"endpoints":["http://localhost:8080/metrics"],"interval":"15s","labels":{"env":"dev"}}`, ""),
		Entry("kubernetes logs", "  collect: {logs: {kubernetes: [{namespace: checkout, podSelector: app=checkout, container: api}]}}",
			"logs", `{"target":{"seam":"host"},"source":"kubernetes","namespace":"checkout","podSelector":"app=checkout","container":"api"}`, ""),
		Entry("loki logs", "  collect: {logs: {loki: [{connection: connection://loki, query: '{app=\"checkout\"}', minLevel: warn}]}}",
			"logs", `{"target":{"seam":"host"},"source":"cloud","backend":"loki","connection":"connection://loki","query":"{app=\"checkout\"}","minLevel":"warn"}`, ""),
		Entry("system_health, attached read-only", "  collect: {sqlserver: {systemHealth: [{connection: connection://db, minDuration: 50ms}]}}",
			"sql_xevent", `{"target":{"seam":"connection"},"connection":"connection://db","session":"system_health","minDuration":"50ms"}`, ""),
		Entry("telltale's own executions", "  collect: {telltale: {exec: [{executable: git, tailBytes: 4096}]}}",
			"exec", `{"target":{"seam":"self"},"name":"git","tailBytes":4096}`, ""),
	)

	It("labels each capture's session with its entry and definition", func() {
		specs, err := definition.Specs(decode(header+"spec: {collect: {go: {gops: [{}], pprof: [{name: heap, profile: heap}]}}}"), definition.Options{})
		Expect(err).ToNot(HaveOccurred())
		Expect(specs).To(HaveEach(HaveField("Labels", HaveKeyWithValue(definition.LabelDefinition, "checkout"))))
		Expect(specs[0].Labels).To(HaveKeyWithValue(definition.LabelEntry, "checkout-collect-go-gops"))
		Expect(specs[1].Labels).To(HaveKeyWithValue(definition.LabelEntry, "heap"))
	})

	It("refuses an option its entry does not know, rather than ignoring it", func() {
		_, err := definition.Decode([]byte(header + "spec: {collect: {go: {gops: [{transport: native}]}}}"))
		Expect(err).To(MatchError(ContainSubstring(`unknown field "transport"`)))
	})
})
