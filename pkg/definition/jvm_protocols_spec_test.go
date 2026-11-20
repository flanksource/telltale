package definition_test

import (
	"encoding/json"

	"github.com/flanksource/clicky/rpc"
	"github.com/flanksource/telltale/pkg/definition"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("JVM protocol definitions", func() {
	It("keeps the optional JVM SQL selection separate from the shared Go SQL schema", func() {
		encoded, err := json.Marshal(rpc.SchemaForStruct(definition.JVMSQLProbe{}))
		Expect(err).NotTo(HaveOccurred())
		var jvm struct {
			Required   []string `json:"required"`
			Properties map[string]struct {
				Default []string `json:"default"`
			} `json:"properties"`
		}
		Expect(json.Unmarshal(encoded, &jvm)).To(Succeed())
		Expect(jvm.Required).NotTo(ContainElement("connections"))
		Expect(jvm.Properties["connections"].Default).To(Equal([]string{"*"}))
		Expect(rpc.SchemaForStruct(definition.SQLProbe{}).Required).To(ContainElement("connections"))
	})

	It("refuses the Go-only SQL self selector in JVM definitions", func() {
		_, err := definition.Specs(decode(header+"spec: {instrument: {jvm: {sql: [{agent: {id: java/checkout}, connections: [self]}]}}}"), definition.Options{})
		Expect(err).To(MatchError(ContainSubstring("self is Go-only")))
	})

	It("defaults omitted JVM SQL connections to every observed connection", func() {
		specs, err := definition.Specs(decode(header+"spec: {instrument: {jvm: {sql: [{agent: {id: java/checkout}}]}}}"), definition.Options{})
		Expect(err).NotTo(HaveOccurred())
		Expect(specs).To(HaveLen(1))
		var params struct {
			Connections []string `json:"connections"`
		}
		Expect(json.Unmarshal(specs[0].Params, &params)).To(Succeed())
		Expect(params.Connections).To(Equal([]string{"*"}))
	})

	DescribeTable("compiles an agent capture with runtime mutation enabled",
		func(entry, kind string) {
			specs, err := definition.Specs(decode(header+"spec: {instrument: {jvm: {"+entry+"}}}"), definition.Options{})
			Expect(err).NotTo(HaveOccurred())
			Expect(specs).To(HaveLen(1))
			Expect(specs[0].Kind).To(Equal(kind))
			var params struct {
				Target struct {
					Seam          string `json:"seam"`
					Agent         string `json:"agent"`
					AllowMutation bool   `json:"allowMutation"`
				} `json:"target"`
			}
			Expect(json.Unmarshal(specs[0].Params, &params)).To(Succeed())
			Expect(params.Target.Seam).To(Equal("agent"))
			Expect(params.Target.Agent).To(Equal("java/checkout"))
			Expect(params.Target.AllowMutation).To(BeTrue())
		},
		Entry("HTTP", "http: [{agent: {id: java/checkout}, features: [http]}]", "http"),
		Entry("HTTP top", "httpTop: [{agent: {id: java/checkout}, direction: incoming}]", "http_top"),
		Entry("SQL", "sql: [{agent: {id: java/checkout}, connections: ['*']}]", "sql"),
		Entry("SQL top without a database connection", "sqlTop: [{agent: {id: java/checkout}, poll: 1s}]", "sql_top"),
		Entry("JMS", "jms: [{agent: {id: java/checkout}, direction: outgoing}]", "jms"),
		Entry("span mirroring", "spans: [{agent: {id: java/checkout}}]", "spans"),
	)
})
