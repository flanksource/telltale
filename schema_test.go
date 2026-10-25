package telltale_test

import (
	"encoding/json"

	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/tracing/httpproxy"
	"github.com/flanksource/telltale/pkg/tracing/pgproxy"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the start form", func() {
	It("describes the command, facets and each armable kind's params behind its enabled switch", func() {
		schema, defaults, err := telltale.StartSchema()
		Expect(err).ToNot(HaveOccurred())
		data, err := json.Marshal(schema)
		Expect(err).ToNot(HaveOccurred())
		var form map[string]any
		Expect(json.Unmarshal(data, &form)).To(Succeed())

		Expect(form).To(HaveKeyWithValue("required", []any{"command"}))
		properties := form["properties"].(map[string]any)
		Expect(properties).To(HaveKeyWithValue("command", HaveKeyWithValue("format", "textarea")))
		Expect(properties).To(HaveKeyWithValue("facets", HaveKeyWithValue("type", "boolean")))
		captures := properties["traces"].(map[string]any)["properties"].(map[string]any)
		Expect(captures).To(HaveLen(2))
		Expect(captures[pgproxy.Kind]).To(HaveKeyWithValue("title", "Postgres proxy"))
		proxy := captures[httpproxy.Kind].(map[string]any)
		Expect(proxy).To(HaveKeyWithValue("title", "HTTP proxy"))
		Expect(proxy["properties"]).To(HaveKeyWithValue("enabled", HaveKeyWithValue("type", "boolean")))
		Expect(proxy["allOf"]).To(ConsistOf(And(
			HaveKeyWithValue("if", map[string]any{"properties": map[string]any{"enabled": map[string]any{"const": true}}}),
			HaveKeyWithValue("then", HaveKeyWithValue("properties", HaveKeyWithValue("mode", And(
				HaveKeyWithValue("enum", ConsistOf("connect", "mitm")),
				HaveKeyWithValue("default", "connect"),
			)))),
		)))

		Expect(defaults).To(Equal(map[string]any{
			"command": "", "facets": false,
			"traces": map[string]any{
				httpproxy.Kind: map[string]any{"enabled": true, "mode": "connect", "bodies": "256KiB"},
				pgproxy.Kind:   map[string]any{"enabled": false, "connection": pgproxy.Kind, "maxStatements": float64(20000)},
			},
		}))
	})
})

var _ = Describe("a start request", func() {
	It("runs its command with sh -c in the work dir, arming the enabled kinds with their params", func() {
		options, err := telltale.StartRequest{
			Command: "curl -sS example.com", Facets: true,
			Traces: map[string]json.RawMessage{
				httpproxy.Kind: json.RawMessage(`{"enabled":true,"mode":"mitm"}`),
				pgproxy.Kind:   json.RawMessage(`{"enabled":false,"connection":""}`),
			},
		}.Options("/work")
		Expect(err).ToNot(HaveOccurred())
		Expect(options).To(Equal(telltale.StartOptions{
			WorkDir: "/work", Dir: "/work", Command: []string{"sh", "-c", "curl -sS example.com"}, Facets: true,
			TraceRoot: telltale.TraceRootCommand,
			Traces:    []telltale.TraceSpec{{Kind: httpproxy.Kind, Params: json.RawMessage(`{"mode":"mitm"}`)}},
		}))
	})

	It("names each field it refuses by its JSON pointer", func() {
		_, err := telltale.StartRequest{
			Command: " ",
			Traces: map[string]json.RawMessage{
				httpproxy.Kind: json.RawMessage(`{"enabled":true,"mode":"sideways"}`),
				"process_tree": json.RawMessage(`{"enabled":true}`),
				pgproxy.Kind:   json.RawMessage(`[]`),
			},
		}.Options("/work")
		var invalid *telltale.ValidationError
		Expect(err).To(BeAssignableToTypeOf(invalid))
		Expect(err.(*telltale.ValidationError).Fields).To(ConsistOf(
			telltale.FieldError{InstancePath: "/command", Message: "a command is required"},
			And(HaveField("InstancePath", "/traces/"+httpproxy.Kind), HaveField("Message", ContainSubstring(`mode "sideways"`))),
			And(HaveField("InstancePath", "/traces/"+pgproxy.Kind), HaveField("Message", ContainSubstring("want an object"))),
			And(HaveField("InstancePath", "/traces/process_tree"), HaveField("Message", ContainSubstring("not a kind a run arms"))),
		))
	})
})
