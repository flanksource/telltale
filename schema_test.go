package telltale_test

import (
	"encoding/json"

	"github.com/flanksource/commons-db/connection/store"
	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/definition"
	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/tracing/httpproxy"
	"github.com/flanksource/telltale/pkg/tracing/pgproxy"
	"github.com/flanksource/telltale/pkg/tracing/target"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
)

var _ = Describe("the start form", func() {
	It("describes a single capture and a nonempty list using the same item schema", func() {
		schema, _, err := telltale.StartSchema()
		Expect(err).NotTo(HaveOccurred())
		branches := schema.Properties["traces"].Properties["logs"].OneOf
		Expect(branches).To(HaveLen(2))
		Expect(branches[0].Type).To(Equal("object"))
		Expect(branches[1].Type).To(Equal("array"))
		Expect(branches[1].Extensions).To(HaveKeyWithValue("minItems", 1))
		Expect(branches[1].Items).To(Equal(branches[0]))
	})

	It("describes the command, the process tree and each armable kind's params behind its enabled switch", func() {
		schema, defaults, err := telltale.StartSchema()
		Expect(err).ToNot(HaveOccurred())
		data, err := json.Marshal(schema)
		Expect(err).ToNot(HaveOccurred())
		var form map[string]any
		Expect(json.Unmarshal(data, &form)).To(Succeed())

		Expect(form).NotTo(HaveKey("required"))
		properties := form["properties"].(map[string]any)
		Expect(properties).To(HaveKeyWithValue("command", HaveKeyWithValue("format", "textarea")))
		Expect(properties).To(HaveKeyWithValue("tree", HaveKeyWithValue("properties", And(HaveKeyWithValue("facets", HaveKeyWithValue("type", "boolean")), HaveKey("environment")))))
		Expect(properties).To(HaveKeyWithValue("pid", HaveKeyWithValue("type", "integer")))
		captures := properties["traces"].(map[string]any)["properties"].(map[string]any)
		logItem := captures["logs"].(map[string]any)["oneOf"].([]any)[0].(map[string]any)
		logFields := logItem["allOf"].([]any)[0].(map[string]any)["then"].(map[string]any)["properties"].(map[string]any)
		Expect(logFields["options"]).To(HaveKeyWithValue("x-telltale-provider-options", HaveKeyWithValue("loki",
			HaveKeyWithValue("properties", HaveKey("direction")))))
		Expect(captures[pgproxy.Kind]).To(HaveKeyWithValue("title", "SQL"))
		proxy := captures[httpproxy.Kind].(map[string]any)["oneOf"].([]any)[0].(map[string]any)
		fields := proxy["allOf"].([]any)[0].(map[string]any)["then"].(map[string]any)["properties"].(map[string]any)
		Expect(fields).To(HaveKey("target"))
		Expect(fields["target"]).To(HaveKeyWithValue("properties", Not(HaveKey("pid"))))
		Expect(captures[httpproxy.Kind]).To(HaveKeyWithValue("title", "HTTP"))
		Expect(proxy["properties"]).To(HaveKeyWithValue("enabled", HaveKeyWithValue("type", "boolean")))
		Expect(proxy["allOf"]).To(ConsistOf(And(
			HaveKeyWithValue("if", map[string]any{"required": []any{"enabled"}, "properties": map[string]any{"enabled": map[string]any{"const": true}}}),
			HaveKeyWithValue("then", HaveKeyWithValue("properties", HaveKeyWithValue("mode", And(
				HaveKeyWithValue("enum", ConsistOf("connect", "mitm")),
				HaveKeyWithValue("default", "connect"),
			)))),
		)))

		captureDefaults := defaults["traces"].(map[string]any)
		Expect(captureDefaults[httpproxy.Kind]).To(And(HaveKeyWithValue("enabled", true), HaveKeyWithValue("mode", "connect"), HaveKeyWithValue("target", map[string]any{"seam": target.Proxy})))
		Expect(captureDefaults[httpproxy.Kind]).NotTo(HaveKey("features"))
		Expect(captureDefaults[pgproxy.Kind]).To(And(HaveKeyWithValue("enabled", false), HaveKeyWithValue("target", map[string]any{"seam": target.Proxy})))
	})

	DescribeTable("publishes each armable kind's summary, seams and default seam on its capture object",
		func(kind string, seams types.GomegaMatcher) {
			registration, err := traces.Registered(kind)
			Expect(err).ToNot(HaveOccurred())
			schema, _, err := telltale.StartSchema()
			Expect(err).ToNot(HaveOccurred())
			data, err := json.Marshal(schema.Properties["traces"].Properties[kind])
			Expect(err).ToNot(HaveOccurred())
			var capture map[string]any
			Expect(json.Unmarshal(data, &capture)).To(Succeed())
			Expect(capture).To(And(
				HaveKeyWithValue("description", registration.Summary),
				HaveKeyWithValue("x-telltale-seams", seams),
				HaveKeyWithValue("x-telltale-default-seam", string(registration.DefaultSeam)),
			))
		},
		Entry("sql_deadlock, aimed only at a connection", "sql_deadlock", Equal([]any{string(target.Connection)})),
		Entry("http, through a proxy or an agent", httpproxy.Kind, ContainElements(string(target.Proxy), string(target.Agent))),
	)

	DescribeTable("looks a SQL Server kind's connection up among sql_server connections",
		func(kind string) {
			kinds, err := traces.Kinds()
			Expect(err).ToNot(HaveOccurred())
			plugin, err := traces.Lookup(kinds, kind)
			Expect(err).ToNot(HaveOccurred())
			params, err := plugin.Schema()
			Expect(err).ToNot(HaveOccurred())
			Expect(params.Properties["connection"].Extensions).To(HaveKeyWithValue("x-clicky-lookup", HaveKeyWithValue("url", "/api/v1/connection?types=sql_server")))
		},
		Entry("sql_deadlock", "sql_deadlock"),
		Entry("sql_xevent", "sql_xevent"),
	)
})

var _ = Describe("a start request", func() {
	var (
		base        telltale.StartOptions
		definitions telltale.Definitions
	)

	BeforeEach(func() {
		connections, err := store.New(store.Layer{Name: store.LayerCWD, Dir: GinkgoT().TempDir()})
		Expect(err).ToNot(HaveOccurred())
		base = telltale.StartOptions{DataDir: "/cache/telltale", Connections: connections}
		definitions, err = definition.NewService(definition.NewStore(nil), connections)
		Expect(err).ToNot(HaveOccurred())
	})

	It("runs its command with sh -c, arming its enabled kinds as the entries of an inline definition", func() {
		options, err := telltale.StartRequest{
			Command: "curl -sS example.com", Tree: traces.TreeOptions{Facets: true},
			Traces: map[string]json.RawMessage{
				httpproxy.Kind: json.RawMessage(`{"enabled":true,"mode":"mitm","target":{"seam":"proxy"}}`),
				pgproxy.Kind:   json.RawMessage(`{"enabled":false,"connection":""}`),
			},
		}.Options(base, definitions)
		Expect(err).ToNot(HaveOccurred())
		Expect(options.Command).To(Equal([]string{"sh", "-c", "curl -sS example.com"}))
		Expect(options.Tree).To(Equal(traces.TreeOptions{Facets: true}))
		Expect(options.TraceRoot).To(Equal(telltale.TraceRootCommand))
		Expect(options.Definition).To(BeNil(), "an ad-hoc start was started from no stored definition")
		Expect(options.Traces).To(HaveExactElements(And(
			HaveField("Kind", httpproxy.Kind),
			HaveField("Params", MatchJSON(`{"mode":"mitm","target":{"seam":"proxy"}}`)),
			HaveField("Labels", map[string]string{definition.LabelEntry: "adhoc-proxy-http", definition.LabelDefinition: definition.InlineName}),
		)))
	})

	It("arms a host kind no start form offers, as a host run", func() {
		options, err := telltale.StartRequest{Traces: map[string]json.RawMessage{
			"prometheus": json.RawMessage(`{"enabled":true,"endpoints":["http://127.0.0.1:9100/metrics"]}`),
		}}.Options(base, definitions)
		Expect(err).ToNot(HaveOccurred())
		Expect(options.Command).To(BeEmpty())
		Expect(options.Traces).To(HaveExactElements(HaveField("Labels", HaveKeyWithValue(definition.LabelEntry, "adhoc-collect-prometheus-scrape"))))
	})

	It("arms each element of a kind's array as its own entry of the inline definition", func() {
		endpoints := []string{"http://127.0.0.1:9100/metrics", "http://127.0.0.1:9200/metrics"}
		options, err := telltale.StartRequest{Traces: map[string]json.RawMessage{
			"prometheus": json.RawMessage(`[{"enabled":true,"endpoints":["` + endpoints[0] + `"]},{"enabled":true,"endpoints":["` + endpoints[1] + `"]}]`),
		}}.Options(base, definitions)
		Expect(err).ToNot(HaveOccurred())
		Expect(options.Traces).To(HaveExactElements(
			And(HaveField("Params", ContainSubstring(endpoints[0])), HaveField("Labels", HaveKeyWithValue(definition.LabelEntry, "adhoc-collect-prometheus-scrape-1"))),
			And(HaveField("Params", ContainSubstring(endpoints[1])), HaveField("Labels", HaveKeyWithValue(definition.LabelEntry, "adhoc-collect-prometheus-scrape-2"))),
		))
	})

	It("names a refused element of a kind's array by its index", func() {
		_, err := telltale.StartRequest{Traces: map[string]json.RawMessage{
			"sql_deadlock": json.RawMessage(`[{"enabled":false},{"enabled":true}]`),
			"prometheus":   json.RawMessage(`[{"enabled":true,"endpoints":["http://127.0.0.1:9100/metrics"]},3]`),
		}}.Options(base, definitions)
		var invalid *telltale.ValidationError
		Expect(err).To(BeAssignableToTypeOf(invalid))
		Expect(err.(*telltale.ValidationError).Fields).To(ConsistOf(
			HaveField("InstancePath", "/traces/sql_deadlock/1/connection"),
			And(HaveField("InstancePath", "/traces/prometheus/1"), HaveField("Message", ContainSubstring("want an object"))),
		))
	})

	It("asks an ad-hoc capture that names no connection for one in its own params", func() {
		_, err := telltale.StartRequest{Traces: map[string]json.RawMessage{
			"sql_deadlock": json.RawMessage(`{"enabled":true}`),
		}}.Options(base, definitions)
		var invalid *telltale.ValidationError
		Expect(err).To(BeAssignableToTypeOf(invalid))
		Expect(err.(*telltale.ValidationError).Fields).To(ConsistOf(telltale.FieldError{
			InstancePath: "/traces/sql_deadlock/connection",
			Message:      "names no connection: set it to a sql_server connection, such as connection://<name>",
		}))
	})

	It("still judges the rest of a capture it refused for naming no connection", func() {
		_, err := telltale.StartRequest{Traces: map[string]json.RawMessage{
			"sql_deadlock": json.RawMessage(`{"enabled":true,"from":"2026-10-28T12:00:00Z","to":"2026-10-28T11:00:00Z"}`),
		}}.Options(base, definitions)
		var invalid *telltale.ValidationError
		Expect(err).To(BeAssignableToTypeOf(invalid))
		Expect(err.(*telltale.ValidationError).Fields).To(ConsistOf(
			HaveField("InstancePath", "/traces/sql_deadlock/connection"),
			And(HaveField("InstancePath", "/traces/sql_deadlock"), HaveField("Message", Not(ContainSubstring("connection")))),
		))
	})

	It("names each field it refuses by its JSON pointer", func() {
		_, err := telltale.StartRequest{
			Command: " ",
			Traces: map[string]json.RawMessage{
				httpproxy.Kind: json.RawMessage(`{"enabled":true,"target":{"seam":"proxy"},"mode":"sideways"}`),
				"process_tree": json.RawMessage(`{"enabled":true}`),
				pgproxy.Kind:   json.RawMessage(`[]`),
			},
		}.Options(base, definitions)
		var invalid *telltale.ValidationError
		Expect(err).To(BeAssignableToTypeOf(invalid))
		Expect(err.(*telltale.ValidationError).Fields).To(ConsistOf(
			telltale.FieldError{InstancePath: "/command", Message: "a command is required"},
			And(HaveField("InstancePath", HavePrefix("/traces/"+httpproxy.Kind)), HaveField("Message", ContainSubstring(`mode "sideways"`))),
			And(HaveField("InstancePath", "/traces/"+pgproxy.Kind), HaveField("Message", ContainSubstring("want an object"))),
			And(HaveField("InstancePath", "/traces/process_tree"), HaveField("Message", ContainSubstring(`unknown trace kind "process_tree"`))),
		))
	})
})
