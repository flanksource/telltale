package cli

import (
	"github.com/flanksource/telltale/pkg/tracing/opensearch"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/spf13/cobra"
)

var _ = Describe("OpenSearch capture arguments", func() {
	It("separates index following from output following and preserves structured search", func() {
		command := &cobra.Command{Use: "test"}
		var run runFlags
		run.bind(command)
		schema, err := opensearch.Kind().Params()
		Expect(err).NotTo(HaveOccurred())
		params, err := bindParamFlags(command.Flags(), schema)
		Expect(err).NotTo(HaveOccurred())
		Expect(command.ParseFlags([]string{"--connection", "traces", "--follow", "--follow-index", "--search", `{"sort":[{"field":"@timestamp","order":"desc"}]}`})).To(Succeed())
		raw, err := params.params()
		Expect(err).NotTo(HaveOccurred())
		Expect(raw).To(MatchJSON(`{"connection":"traces","followIndex":true,"search":{"sort":[{"field":"@timestamp","order":"desc"}]}}`))
	})
})
