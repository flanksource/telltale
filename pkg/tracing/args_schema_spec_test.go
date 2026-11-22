package tracing_test

import (
	"encoding/json"

	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/logs"
	"github.com/flanksource/telltale/pkg/tracing/spans"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

func compileArgumentSchema(plugin tracing.TracePlugin) *jsonschema.Schema {
	GinkgoHelper()
	args, err := plugin.Schema()
	Expect(err).NotTo(HaveOccurred())
	raw, err := json.Marshal(args)
	Expect(err).NotTo(HaveOccurred())
	var document any
	Expect(json.Unmarshal(raw, &document)).To(Succeed())
	compiler := jsonschema.NewCompiler()
	const location = "https://example.test/arguments.json"
	Expect(compiler.AddResource(location, document)).To(Succeed())
	schema, err := compiler.Compile(location)
	Expect(err).NotTo(HaveOccurred())
	return schema
}

var _ = Describe("kind argument schemas", func() {
	DescribeTable("describes valid and invalid host log options", func(raw string, valid bool) {
		var value any
		Expect(json.Unmarshal([]byte(raw), &value)).To(Succeed())
		Expect(compileArgumentSchema(logs.NewKind(nil)).Validate(value) == nil).To(Equal(valid))
	},
		Entry("container logs with a name", `{"target":{"seam":"host"},"source":"container","container":"orders"}`, true),
		Entry("host logs without a source", `{"target":{"seam":"host"}}`, false),
		Entry("pod logs without a selector", `{"target":{"seam":"host"},"source":"kubernetes","namespace":"orders"}`, false),
		Entry("a host source on an agent", `{"target":{"seam":"agent","agent":"orders"},"source":"kubernetes","namespace":"orders","podSelector":"app=orders"}`, false),
		Entry("a provider's typed scalar options", `{"target":{"seam":"host"},"source":"cloud","backend":"loki","connection":"connection://orders","options":{"direction":"forward"}}`, true),
		Entry("cloud logs without a connection", `{"target":{"seam":"host"},"source":"cloud","backend":"loki"}`, false),
		Entry("a raw and structured OpenSearch query together", `{"target":{"seam":"host"},"source":"cloud","backend":"opensearch","connection":"connection://search","query":"*","options":{"search":{}}}`, false),
	)

	DescribeTable("describes host span arguments", func(raw string, valid bool) {
		var value any
		Expect(json.Unmarshal([]byte(raw), &value)).To(Succeed())
		Expect(compileArgumentSchema(spans.NewKind(nil)).Validate(value) == nil).To(Equal(valid))
	},
		Entry("an OpenTelemetry connection and explicit span format", `{"target":{"seam":"host"},"opensearch":{"connection":"connection://orders","format":"jaeger"}}`, true),
		Entry("host spans without OpenSearch options", `{"target":{"seam":"host"}}`, false),
		Entry("non-host spans with OpenSearch options", `{"opensearch":{"connection":"connection://orders"}}`, false),
		Entry("an unbounded follow with an end time", `{"target":{"seam":"host"},"opensearch":{"connection":"connection://orders","followIndex":true,"to":"now"}}`, false),
	)
})
