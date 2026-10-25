package cli

import (
	"github.com/flanksource/clicky/rpc"
	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/tracing/httpproxy"
	"github.com/flanksource/telltale/pkg/tracing/process"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/spf13/pflag"
)

// kindSchema is the params schema of a registered kind.
func kindSchema(kind string) *rpc.OpenAPISchema {
	kinds, err := traces.Kinds()
	Expect(err).ToNot(HaveOccurred())
	plugin, err := traces.Lookup(kinds, kind)
	Expect(err).ToNot(HaveOccurred())
	schema, err := plugin.Params()
	Expect(err).ToNot(HaveOccurred())
	return schema
}

// parsed binds schema's flags and parses args into its params.
func parsed(schema *rpc.OpenAPISchema, args ...string) (string, error) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	bound, err := bindParamFlags(flags, schema)
	Expect(err).ToNot(HaveOccurred())
	Expect(flags.Parse(args)).To(Succeed())
	params, err := bound.params()
	return string(params), err
}

var _ = Describe("a kind's params as flags", func() {
	It("binds every registered kind's params", func() {
		for _, registration := range traces.Registrations() {
			_, err := bindParamFlags(pflag.NewFlagSet(registration.Name, pflag.ContinueOnError), kindSchema(registration.Name))
			Expect(err).ToNot(HaveOccurred(), registration.Name)
		}
	})

	DescribeTable("names each flag by its param's JSON path in kebab case",
		func(path []string, expected string) { Expect(flagName(path)).To(Equal(expected)) },
		Entry("a camel-case name", []string{"noProxy"}, "no-proxy"),
		Entry("a trailing acronym", []string{"upstreamCA"}, "upstream-ca"),
		Entry("a nested param", []string{"environment", "keys"}, "environment-keys"),
		Entry("a lowercase name", []string{"mode"}, "mode"),
	)

	DescribeTable("sets only the params whose flags were given, with their types",
		func(kind string, args []string, expected string) {
			params, err := parsed(kindSchema(kind), args...)
			Expect(err).ToNot(HaveOccurred())
			if expected == "" {
				Expect(params).To(BeEmpty())
				return
			}
			Expect(params).To(MatchJSON(expected))
		},
		Entry("none, so the kind's defaults apply", httpproxy.Kind, []string{}, ""),
		Entry("a choice and a list", httpproxy.Kind, []string{"--mode", "mitm", "--hosts", "a.example,b.example"}, `{"mode": "mitm", "hosts": ["a.example", "b.example"]}`),
		Entry("a repeated list flag", httpproxy.Kind, []string{"--redact", "X-One", "--redact", "X-Two"}, `{"redact": ["X-One", "X-Two"]}`),
		Entry("a boolean and an integer", "pg_proxy", []string{"--params", "--max-statements", "10"}, `{"params": true, "maxStatements": 10}`),
		Entry("an object's params, nested", process.TreeKind, []string{"--environment-keys", "PATH", "--interval", "2s"}, `{"environment": {"keys": ["PATH"]}, "interval": "2s"}`),
		Entry("an embedded struct's params, flat", process.SamplesKind, []string{"--root-pid", "7", "--facets", "ports"}, `{"rootPid": 7, "facets": ["ports"]}`),
	)

	It("refuses a value outside a param's choices, naming its flag and the choices", func() {
		_, err := parsed(kindSchema(httpproxy.Kind), "--mode", "sideways")
		Expect(err).To(MatchError(`--mode: "sideways" is not one of connect, mitm`))
	})

	It("shows a param's description, choices and default in its usage", func() {
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		_, err := bindParamFlags(flags, kindSchema(httpproxy.Kind))
		Expect(err).ToNot(HaveOccurred())
		mode := flags.Lookup("mode")
		Expect(mode.Usage).To(Equal("connect records HTTPS tunnels by host; mitm decrypts and records each exchange (one of connect, mitm)"))
		Expect(mode.DefValue).To(Equal("connect"))
	})

	DescribeTable("refuses a schema it cannot bind",
		func(schema *rpc.OpenAPISchema, refusal string) {
			flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
			flags.String("work-dir", ".", "taken")
			_, err := bindParamFlags(flags, schema)
			Expect(err).To(MatchError(refusal))
		},
		Entry("a list of objects", &rpc.OpenAPISchema{Type: "object", Properties: map[string]*rpc.OpenAPISchema{
			"rules": {Type: "array", Items: &rpc.OpenAPISchema{Type: "object"}},
		}}, "param rules: a list of object param cannot be a flag"),
		Entry("a param whose flag is taken", &rpc.OpenAPISchema{Type: "object", Properties: map[string]*rpc.OpenAPISchema{
			"workDir": {Type: "string"},
		}}, "param workDir: its flag --work-dir is already defined"),
	)
})
