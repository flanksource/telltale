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
)

var _ = Describe("the start form", func() {
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
		Expect(captures[pgproxy.Kind]).To(HaveKeyWithValue("title", "SQL"))
		proxy := captures[httpproxy.Kind].(map[string]any)
		fields := proxy["allOf"].([]any)[0].(map[string]any)["then"].(map[string]any)["properties"].(map[string]any)
		Expect(fields).To(HaveKey("target"))
		Expect(fields["target"]).To(HaveKeyWithValue("properties", Not(HaveKey("pid"))))
		Expect(proxy).To(HaveKeyWithValue("title", "HTTP"))
		Expect(proxy["properties"]).To(HaveKeyWithValue("enabled", HaveKeyWithValue("type", "boolean")))
		Expect(proxy["allOf"]).To(ConsistOf(And(
			HaveKeyWithValue("if", map[string]any{"properties": map[string]any{"enabled": map[string]any{"const": true}}}),
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
			And(HaveField("InstancePath", "/traces/process_tree"), HaveField("Message", ContainSubstring("no trace definition list arms process_tree"))),
		))
	})
})
