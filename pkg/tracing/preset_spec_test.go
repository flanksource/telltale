// Specs for seam presets: a kind registered under its own name that arms its
// base kind on one fixed target seam, and host defaults that name a seam.

package tracing_test

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/recordstore/recordresults"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/target"
	"github.com/flanksource/telltale/pkg/tracing/tracingtest"
)

type seamParams struct {
	Target target.Target `json:"target,omitzero"`
	Label  string        `json:"label,omitempty"`
}

// seamTicker emits one tick naming the seam it was armed on and its label.
type seamTicker struct{}

func (seamTicker) Params() seamParams { return seamParams{Label: "base"} }

func (seamTicker) Schema() recordresults.ResultType[tick] {
	return recordresults.ResultType[tick]{Title: "Seams", TimeColumn: "at"}
}

func (seamTicker) Handle(ctx dbcontext.Context, p seamParams, records tracing.Emitter[tick], _ tracing.Records[tick]) error {
	return records.Emit(ctx, tick{At: time.Now().UTC(), N: 1, Label: string(p.Target.Seam) + ":" + p.Label})
}

func seamPlugin() *tracing.Handler[seamParams, tick] {
	return tracing.NewHandler[seamParams, tick](seamTicker{}, tracing.Capabilities{Historical: true}, target.Self, target.Proxy)
}

func proxyPreset() *tracing.Handler[seamParams, tick] {
	return seamPlugin().Preset(tracing.PresetOptions[seamParams]{
		Title: "Proxy seams", Seam: target.Proxy,
		Defaults: func(p *seamParams) { p.Label = "preset" },
	})
}

var _ = Describe("Seam defaults", func() {
	It("arms a capture with no target on the seam the host's defaults name", func() {
		plugin := seamPlugin().WithDefaults(seamParams{Target: target.Target{Seam: target.Proxy}, Label: "host"})
		seams, defaultSeam := plugin.TargetSeams()
		Expect(seams).To(Equal([]target.Seam{target.Self, target.Proxy}))
		Expect(defaultSeam).To(Equal(target.Proxy))
		schema, err := plugin.Params()
		Expect(err).NotTo(HaveOccurred())
		Expect(schema.Properties["target"].Properties["seam"].Default).To(Equal("proxy"))

		env := tracingtest.NewEnv(map[string]tracing.TracePlugin{"seams": plugin})
		Expect(env.Rows(tracingtest.Ended(env.Start("seams", `{}`)))).To(ConsistOf(HaveKeyWithValue("label", "proxy:host")))
		Expect(env.Rows(tracingtest.Ended(env.Start("seams", `{"target":{"seam":"self"}}`)))).To(ConsistOf(HaveKeyWithValue("label", "self:host")))
	})

	It("refuses defaults naming a seam the kind does not support", func() {
		plugin := seamPlugin().WithDefaults(seamParams{Target: target.Target{Seam: target.Host}})
		Expect(tracing.NewKinds().RegisterKind("seams", plugin)).To(MatchError(And(ContainSubstring(`"host"`), ContainSubstring("[self proxy]"))))
	})
})

var _ = Describe("Seam presets", func() {
	var kinds *tracing.Kinds

	BeforeEach(func() {
		kinds = tracing.NewKinds()
		Expect(kinds.RegisterKind("seams", seamPlugin())).To(Succeed())
		Expect(kinds.RegisterKind("proxyseams", proxyPreset())).To(Succeed())
	})

	It("lists the preset as its own kind, its form offering only the fixed seam", func() {
		infos, err := kinds.List()
		Expect(err).NotTo(HaveOccurred())
		Expect(infos).To(HaveLen(2))
		preset := infos[0]
		Expect(preset.Name).To(Equal("proxyseams"))
		Expect(preset.Title).To(Equal("Proxy seams"))
		Expect(preset.Columns).To(Equal(infos[1].Columns))
		seam := preset.Params.Properties["target"].Properties["seam"]
		Expect(seam.Enum).To(Equal([]any{"proxy"}))
		Expect(seam.Default).To(Equal("proxy"))
		Expect(preset.Params.Properties["label"].Default).To(Equal("preset"))
		Expect(infos[1].Params.Properties["label"].Default).To(Equal("base"))
	})

	It("starts on the fixed seam and stores the base kind's records under the preset's name", func() {
		plugin := proxyPreset()
		env := tracingtest.NewEnv(map[string]tracing.TracePlugin{"proxyseams": plugin})
		info := tracingtest.Ended(env.Start("proxyseams", `{"target":{"seam":"proxy"},"label":"asked"}`))
		Expect(env.Rows(info)).To(ConsistOf(HaveKeyWithValue("label", "proxy:asked")))

		var read []tick
		Expect(tracing.ReadRecords(context.Background(), env.Runtime, tracing.TraceRef{Kind: "proxyseams", Stream: info.Events.Stream}, func(record tick) error {
			read = append(read, record)
			return nil
		})).To(Succeed())
		Expect(read).To(ConsistOf(HaveField("Label", "proxy:asked")))
		Expect(env.Rows(tracingtest.Ended(env.Start("proxyseams", `{}`)))).To(ConsistOf(HaveKeyWithValue("label", "proxy:preset")))
	})

	It("refuses a start naming another seam", func() {
		plugin, _ := kinds.Get("proxyseams")
		err := plugin.ValidateParams(json.RawMessage(`{"target":{"seam":"self"}}`))
		Expect(errors.Is(err, tracing.ErrPresetSeam)).To(BeTrue(), "%v", err)
		Expect(err).To(MatchError(And(ContainSubstring(`"proxyseams"`), ContainSubstring(`"proxy"`), ContainSubstring(`"self"`))))
	})

	DescribeTable("refuses a preset it could not serve",
		func(preset func() tracing.TracePlugin, message string) {
			Expect(tracing.NewKinds().RegisterKind("broken", preset())).To(MatchError(ContainSubstring(message)))
		},
		Entry("a seam the base kind does not support", func() tracing.TracePlugin {
			return seamPlugin().Preset(tracing.PresetOptions[seamParams]{Title: "Hosts", Seam: target.Host})
		}, `preset seam "host"`),
		Entry("no seam", func() tracing.TracePlugin {
			return seamPlugin().Preset(tracing.PresetOptions[seamParams]{Title: "None"})
		}, "preset needs a seam"),
		Entry("no title", func() tracing.TracePlugin {
			return seamPlugin().Preset(tracing.PresetOptions[seamParams]{Seam: target.Proxy})
		}, "preset needs a title"),
		Entry("defaults naming another seam", func() tracing.TracePlugin {
			return seamPlugin().Preset(tracing.PresetOptions[seamParams]{Title: "Self", Seam: target.Proxy, Defaults: func(p *seamParams) { p.Target.Seam = target.Self }})
		}, `defaults name seam "self"`),
		Entry("a base kind with result tables", func() tracing.TracePlugin {
			return seamPlugin().WithResults(tracing.NewResultTable("metrics", ticker{}.Schema())).
				Preset(tracing.PresetOptions[seamParams]{Title: "Tables", Seam: target.Proxy})
		}, "result tables"),
	)
})
