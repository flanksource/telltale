package cli

import (
	"github.com/flanksource/clicky/rpc"
	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/tracing/goprof"
	"github.com/flanksource/telltale/pkg/tracing/httpproxy"
	"github.com/flanksource/telltale/pkg/tracing/process"
	"github.com/flanksource/telltale/pkg/tracing/target"
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
	It("builds a CPU checkpoint from profile flags", func() {
		flags := pflag.NewFlagSet("goprof", pflag.ContinueOnError)
		checkpoint := bindProfileCheckpoint(flags, goprof.CheckpointParams{Source: goprof.SourceGops, Profile: "heap"})
		Expect(flags.Parse([]string{"--profile", "cpu", "--seconds", "1"})).To(Succeed())
		data, err := checkpoint.params()
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(MatchJSON(`{"source":"gops","profile":"cpu","seconds":1}`))
	})
	It("binds every registered kind's params", func() {
		for _, registration := range traces.Registrations() {
			_, err := bindParamFlags(pflag.NewFlagSet(registration.Name, pflag.ContinueOnError), kindSchema(registration.Name))
			Expect(err).ToNot(HaveOccurred(), registration.Name)
		}
	})

	It("shows the supported seam but does not offer unimplemented agent targeting", func() {
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		_, err := bindParamFlags(flags, kindSchema(process.SamplesKind))
		Expect(err).NotTo(HaveOccurred())
		Expect(flags.Lookup("seam").Usage).To(ContainSubstring("process"))
		Expect(flags.Lookup("agent")).To(BeNil())
		Expect(flags.Lookup("selector")).To(BeNil())
		Expect(flags.Lookup("wait")).To(BeNil())
	})
	It("names the selector flag when its value is malformed", func() {
		schema := kindSchema(process.SamplesKind)
		schema.Properties["target"].Extensions["x-telltale-seams"] = []target.Seam{target.Agent, target.Process}
		_, err := parsed(schema, "--selector", "invalid")
		Expect(err).To(MatchError(ContainSubstring("--selector:")))
	})
	It("keeps a run PID separate from an explicit agent", func() {
		schema := kindSchema(process.SamplesKind)
		schema.Properties["target"].Extensions["x-telltale-seams"] = []target.Seam{target.Agent, target.Process}
		params, err := parsed(schema, "--pid", "123", "--agent", "worker")
		Expect(err).NotTo(HaveOccurred())
		Expect(params).To(MatchJSON(`{"target":{"agent":"worker"}}`))
	})
	It("passes mutation consent in the common target", func() {
		schema := kindSchema(process.SamplesKind)
		schema.Properties["target"].Extensions["x-telltale-seams"] = []target.Seam{target.Agent, target.Process}
		params, err := parsed(schema, "--allow-mutation")
		Expect(err).NotTo(HaveOccurred())
		Expect(params).To(MatchJSON(`{"target":{"allowMutation":true}}`))
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
		Entry("a boolean and an integer", "sql", []string{"--params", "--max-statements", "10"}, `{"params": true, "maxStatements": 10}`),
		Entry("an object's params, nested", process.TreeKind, []string{"--environment-keys", "PATH", "--interval", "2s"}, `{"environment": {"keys": ["PATH"]}, "interval": "2s"}`),
		Entry("a run PID and an embedded struct's params", process.SamplesKind, []string{"--pid", "7", "--facets", "ports"}, `{"facets": ["ports"]}`),
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
