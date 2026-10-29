package cli

import (
	"github.com/flanksource/clicky/flags"
	"github.com/flanksource/clicky/rpc"
	"github.com/flanksource/telltale/pkg/tracing/goprof"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var _ = Describe("schema-driven run arguments", func() {
	It("publishes enum metadata for scalar and list arguments", func() {
		command := &cobra.Command{Use: "test"}
		schema := &rpc.OpenAPISchema{Type: "object", Properties: map[string]*rpc.OpenAPISchema{
			"mode":     {Type: "string", Enum: []any{"fast", "full"}},
			"features": {Type: "array", Items: &rpc.OpenAPISchema{Type: "string", Enum: []any{"http", "sql"}}},
		}}
		_, err := bindParamFlags(command.Flags(), schema)
		Expect(err).NotTo(HaveOccurred())
		Expect(flags.AllowedValues(command.Flags().Lookup("mode"))).To(Equal([]string{"fast", "full"}))
		Expect(flags.AllowedValues(command.Flags().Lookup("features"))).To(Equal([]string{"http", "sql"}))
	})

	It("keeps list and nested object defaults in help and typed flag values", func() {
		set := pflag.NewFlagSet("test", pflag.ContinueOnError)
		_, err := bindParamFlags(set, &rpc.OpenAPISchema{Type: "object", Properties: map[string]*rpc.OpenAPISchema{
			"labels": {Type: "array", Items: &rpc.OpenAPISchema{Type: "string"}, Default: []any{"worker", "api"}},
			"environment": {Type: "object", Default: map[string]any{"keys": []any{"PATH"}}, Properties: map[string]*rpc.OpenAPISchema{
				"keys": {Type: "array", Items: &rpc.OpenAPISchema{Type: "string"}},
			}},
			"count": {Type: "integer", Default: 7},
		}})
		Expect(err).NotTo(HaveOccurred())
		Expect(set.Lookup("labels").DefValue).To(Equal("[worker,api]"))
		Expect(set.Lookup("environment-keys").DefValue).To(Equal("[PATH]"))
		Expect(set.Lookup("count").DefValue).To(Equal("7"))
	})

	It("marks required arguments without requiring supplied defaults", func() {
		command := &cobra.Command{Use: "test"}
		_, err := bindParamFlags(command.Flags(), &rpc.OpenAPISchema{Type: "object", Required: []string{"name", "count"}, Properties: map[string]*rpc.OpenAPISchema{
			"name":  {Type: "string", Description: "Capture name"},
			"count": {Type: "integer", Default: float64(0)},
		}})
		Expect(err).NotTo(HaveOccurred())
		Expect(command.ValidateRequiredFlags()).To(MatchError(ContainSubstring("name")))
		Expect(command.Flags().Set("name", "worker")).To(Succeed())
		Expect(command.ValidateRequiredFlags()).To(Succeed())
		Expect(flagDescription(command.Flags().Lookup("name"))).To(ContainSubstring("required"))
	})

	It("requires children of an optional object only when that object is supplied", func() {
		schema := &rpc.OpenAPISchema{Type: "object", Properties: map[string]*rpc.OpenAPISchema{
			"environment": {Type: "object", Required: []string{"name"}, Properties: map[string]*rpc.OpenAPISchema{
				"name": {Type: "string"}, "value": {Type: "string"},
			}},
		}}
		_, err := parsed(schema)
		Expect(err).NotTo(HaveOccurred())
		_, err = parsed(schema, "--environment-value", "worker")
		Expect(err).To(MatchError(ContainSubstring("--environment-name")))
		value, err := parsed(schema, "--environment-name", "MODE", "--environment-value", "worker")
		Expect(err).NotTo(HaveOccurred())
		Expect(value).To(MatchJSON(`{"environment":{"name":"MODE","value":"worker"}}`))
	})

	DescribeTable("refuses invalid defaults rather than silently using zero values", func(property *rpc.OpenAPISchema) {
		_, err := bindParamFlags(pflag.NewFlagSet("test", pflag.ContinueOnError), &rpc.OpenAPISchema{Type: "object", Properties: map[string]*rpc.OpenAPISchema{"value": property}})
		Expect(err).To(MatchError(ContainSubstring("param value")))
	},
		Entry("wrong scalar type", &rpc.OpenAPISchema{Type: "integer", Default: "seven"}),
		Entry("fractional integer", &rpc.OpenAPISchema{Type: "integer", Default: 1.5}),
		Entry("wrong list item type", &rpc.OpenAPISchema{Type: "array", Items: &rpc.OpenAPISchema{Type: "string"}, Default: []any{7}}),
	)

	It("validates each supplied list item against its schema choices", func() {
		schema := &rpc.OpenAPISchema{Type: "object", Properties: map[string]*rpc.OpenAPISchema{
			"features": {Type: "array", Items: &rpc.OpenAPISchema{Type: "string", Enum: []any{"http", "sql"}}},
		}}
		value, err := parsed(schema, "--features", "http,sql")
		Expect(err).NotTo(HaveOccurred())
		Expect(value).To(MatchJSON(`{"features":["http","sql"]}`))
		_, err = parsed(schema, "--features", "http,invalid")
		Expect(err).To(MatchError(ContainSubstring("--features")))
	})

	It("preserves explicit false and zero overrides", func() {
		value, err := parsed(&rpc.OpenAPISchema{Type: "object", Properties: map[string]*rpc.OpenAPISchema{
			"enabled": {Type: "boolean", Default: true}, "count": {Type: "integer", Default: float64(7)},
		}}, "--enabled=false", "--count=0")
		Expect(err).NotTo(HaveOccurred())
		Expect(value).To(MatchJSON(`{"enabled":false,"count":0}`))
	})

	It("binds string maps as assignments and preserves their JSON shape", func() {
		schema := &rpc.OpenAPISchema{Type: "object", Properties: map[string]*rpc.OpenAPISchema{
			"levels": {Type: "object", AdditionalProperties: &rpc.OpenAPISchema{Type: "string"}, Default: map[string]string{"worker": "info"}},
		}}
		set := pflag.NewFlagSet("test", pflag.ContinueOnError)
		_, err := bindParamFlags(set, schema)
		Expect(err).NotTo(HaveOccurred())
		Expect(set.Lookup("levels").DefValue).To(Equal("[worker=info]"))
		value, err := parsed(schema, "--levels", "worker=debug,api=error")
		Expect(err).NotTo(HaveOccurred())
		Expect(value).To(MatchJSON(`{"levels":{"worker":"debug","api":"error"}}`))
		_, err = parsed(schema, "--levels", "invalid")
		Expect(err).To(MatchError(ContainSubstring("--levels")))
		_, err = parsed(schema, "--levels", "worker=debug,worker=error")
		Expect(err).To(MatchError(ContainSubstring("repeated")))
	})

	It("binds arbitrary map values as JSON without losing numeric precision", func() {
		schema := &rpc.OpenAPISchema{Type: "object", Properties: map[string]*rpc.OpenAPISchema{
			"options": {Type: "object", AdditionalProperties: &rpc.OpenAPISchema{Type: "object"}},
		}}
		value, err := parsed(schema, "--options", `{"limit":9007199254740993,"enabled":false,"nested":{"name":"worker"}}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(value).To(ContainSubstring("9007199254740993"))
		Expect(value).To(MatchJSON(`{"options":{"limit":9007199254740993,"enabled":false,"nested":{"name":"worker"}}}`))
		for _, invalid := range []string{"[]", "null", "invalid"} {
			_, err := parsed(schema, "--options", invalid)
			Expect(err).To(MatchError(ContainSubstring("--options")))
		}
	})

	It("publishes the supported target seams and their default", func() {
		seam := kindSchema("http").Properties["target"].Properties["seam"]
		Expect(enumOf(seam)).To(Equal([]string{"self", "agent", "proxy"}))
		Expect(seam.Default).To(Equal("self"))
	})

	It("uses checkpoint schema choices and retains the registered payload", func() {
		set := pflag.NewFlagSet("goprof", pflag.ContinueOnError)
		checkpoint := bindProfileCheckpoint(set, goprof.CheckpointParams{Source: goprof.SourceGops, Profile: "heap", Reason: "interval"})
		Expect(flags.AllowedValues(set.Lookup("profile"))).To(Equal(goprof.Profiles))
		Expect(flags.AllowedValues(set.Lookup("source"))).To(Equal([]string{goprof.SourcePProf, goprof.SourceGops, goprof.SourceFile}))
		Expect(set.Parse([]string{"--profile", "cpu", "--seconds", "1"})).To(Succeed())
		data, err := checkpoint.params()
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(MatchJSON(`{"source":"gops","profile":"cpu","seconds":1,"reason":"interval"}`))
	})

	DescribeTable("refuses invalid checkpoint overrides", func(args []string, refusal string) {
		set := pflag.NewFlagSet("goprof", pflag.ContinueOnError)
		checkpoint := bindProfileCheckpoint(set, goprof.CheckpointParams{Source: goprof.SourceGops, Profile: "heap"})
		Expect(set.Parse(args)).To(Succeed())
		_, err := checkpoint.params()
		Expect(err).To(MatchError(ContainSubstring(refusal)))
	},
		Entry("unknown profile", []string{"--profile", "invalid"}, "--profile"),
		Entry("unknown source", []string{"--source", "invalid"}, "--source"),
		Entry("seconds on a heap profile", []string{"--seconds", "1"}, "--seconds"),
		Entry("negative seconds", []string{"--profile", "cpu", "--seconds", "-1"}, "--seconds"),
	)
})
