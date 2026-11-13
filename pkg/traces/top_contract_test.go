package traces_test

import (
	"context"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/commons-db/recordstore/recordresults"
	"github.com/flanksource/commons-db/recordstore/recordresults/recordresultstest"
	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/tracing"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("catalog table contracts", func() {
	It("advertises Top for all eight sampled kinds and validates every table's native bindings", func() {
		kinds, err := traces.Kinds()
		Expect(err).NotTo(HaveOccurred())
		for _, name := range []string{"sql_top", "sql_plancache", "process", "goprof", "jvm_jfr", "prometheus", "stacks", "query"} {
			plugin, ok := kinds.Get(name)
			Expect(ok).To(BeTrue(), name)
			Expect(tracing.TopOf(plugin)).NotTo(BeNil(), name)
		}
		results := recordresultstest.OpenResults(recordresults.OpenOptions{Prefix: "traces", ConnectionName: "traces", Settings: recordresultstest.LocalSettings(recordstore.BackendSQLite), Register: kinds.RegisterResultTypes})
		for _, registered := range results.Registry.ResultTypes() {
			profile, err := results.Registry.Get(context.Background(), registered.Profile)
			Expect(err).NotTo(HaveOccurred(), registered.Kind)
			columns, err := query.ClickyColumns(profile)
			Expect(err).NotTo(HaveOccurred(), registered.Kind)
			for _, column := range columns {
				if column.FilterKey != "" {
					_, err := query.ResolveColumnFilters(profile, map[string]any{})
					Expect(err).NotTo(HaveOccurred(), registered.Kind+"/"+column.Name)
				}
				if column.SortKey != "" {
					_, err := profile.RequestedOrder(column.SortKey, true)
					Expect(err).NotTo(HaveOccurred(), registered.Kind+"/"+column.Name)
				}
			}
		}
	})
})
