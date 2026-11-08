package definition_test

import (
	"encoding/json"
	"slices"
	"strings"

	dbquery "github.com/flanksource/commons-db/query"
	_ "github.com/flanksource/commons-db/query/providers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/flanksource/telltale/pkg/definition"
	"github.com/flanksource/telltale/pkg/traces"
)

// path is a list's path, section.group.type.
func path(at definition.Located) string {
	return strings.Join(slices.DeleteFunc([]string{at.Section, at.Group, at.Type}, func(part string) bool { return part == "" }), ".")
}

// logProviders are the providers collect.logs reads, by the list that reads
// each; kubernetes logs are read with the local client, as the k8s provider's.
var logProviders = map[string]string{
	"k8s": "collect.logs.kubernetes", "loki": "collect.logs.loki", "cloudwatch": "collect.logs.cloudwatch",
	"opensearch": "collect.logs.opensearch", "gcpcloudlogging": "collect.logs.gcpCloudLogging", "azureloganalytics": "collect.logs.azureLogAnalytics",
}

// sqlEngines are the providers collect.query.sql reads through sql.type.
var sqlEngines = []string{"sql", "postgres", "mysql", "sqlserver", "clickhouse", "sqlite"}

// kindProviders are the providers registered for something other than a
// source a definition reads: the trace store and the XE sessions
// instrument.sqlserver.xevent and collect.sqlserver.systemHealth record, read
// back by telltale's own kinds, and the profile store's legacy-trace
// provider, which keeps old trace profiles listable and executes nothing.
var kindProviders = []string{"recordstore", "sqlserver-xevent", "legacy-trace"}

var _ = Describe("the typed lists", func() {
	lists := definition.Lists()

	It("map every registered kind to a list", func() {
		covered := map[string]bool{}
		for _, at := range lists {
			covered[at.Kind] = true
		}
		for _, registration := range traces.Registrations() {
			Expect(covered).To(HaveKey(registration.Name), "no list compiles to %s", registration.Name)
		}
	})

	It("compile only to registered kinds", func() {
		var unregistered []string
		for _, at := range lists {
			if _, err := traces.Registered(at.Kind); err != nil {
				unregistered = append(unregistered, path(at)+" → "+at.Kind)
			}
		}
		Expect(unregistered).To(BeEmpty())
	})

	It("read every commons-db provider in collect.logs or collect.query, opensearch alone in both", func() {
		queried := map[string]bool{}
		for _, at := range lists {
			if at.Provider != "" {
				queried[at.Provider] = true
			}
		}
		for _, engine := range sqlEngines {
			queried[engine] = queried["sql"]
		}
		var uncovered, both []string
		for _, provider := range dbquery.RegisteredProviders() {
			_, logged := logProviders[provider]
			switch {
			case logged && queried[provider]:
				both = append(both, provider)
			case !logged && !queried[provider] && !slices.Contains(kindProviders, provider):
				uncovered = append(uncovered, provider)
			}
		}
		Expect(uncovered).To(BeEmpty())
		Expect(both).To(ConsistOf("opensearch"))
		for provider, list := range logProviders {
			Expect(dbquery.RegisteredProviders()).To(ContainElement(provider))
			Expect(slices.ContainsFunc(lists, func(at definition.Located) bool { return path(at) == list })).To(BeTrue(), list)
		}
	})

	It("are each held by the every-kind fixture", func() {
		held := map[string]bool{}
		for _, at := range definition.Walk(&load("testdata/all-kinds.yaml").Spec) {
			held[path(at)] = true
		}
		for _, at := range lists {
			Expect(held).To(HaveKey(path(at)))
		}
	})
})

var _ = Describe("a compiled collect entry", func() {
	It("never changes the target: no mutation, no mutex or block profile, no managed XE session", func() {
		def := load("testdata/all-kinds.yaml")
		// Query entries read a provider over a window and carry no runtime to
		// change; they are held to registration by the typed-lists guard.
		def.Spec.Collect.Query = definition.CollectQuery{}
		resolved, err := definition.Resolve(def, definition.Options{})
		Expect(err).ToNot(HaveOccurred())
		specs, err := definition.Specs(def, definition.Options{})
		Expect(err).ToNot(HaveOccurred())
		Expect(specs).To(HaveLen(len(resolved.Entries)))
		for i, at := range resolved.Entries {
			var params struct {
				Target struct {
					AllowMutation bool `json:"allowMutation"`
				} `json:"target"`
				Session string `json:"session"`
			}
			Expect(json.Unmarshal(specs[i].Params, &params)).To(Succeed())
			var checkpoint struct {
				Profile string `json:"profile"`
			}
			if specs[i].Checkpoint != nil {
				Expect(json.Unmarshal(specs[i].Checkpoint, &checkpoint)).To(Succeed())
			}
			if at.Section != definition.SectionCollect {
				Expect(params.Target.AllowMutation).To(Equal(at.Section == definition.SectionInstrument), at.Name)
				continue
			}
			Expect(params.Target.AllowMutation).To(BeFalse(), at.Name)
			Expect(checkpoint.Profile).ToNot(BeElementOf("mutex", "block"), at.Name)
			if at.Kind == "sql_xevent" {
				Expect(params.Session).To(Equal("system_health"), at.Name)
			}
		}
	})

	It("refuses levels on agent logs, which would change the agent's log levels", func() {
		err := definition.Validate(decode(`
apiVersion: telltale.flanksource.com/v1alpha1
kind: TraceDefinition
metadata: {name: levels}
spec:
  collect:
    go:
      logs:
        - levels: {root: debug}
`), definition.Options{})
		Expect(err).To(MatchError(ContainSubstring("/spec/collect/go/logs/0: levels sets the agent's log levels")))
	})
})
