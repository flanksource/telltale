package jvmruntime_test

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/flanksource/telltale/pkg/tracing/jvm/jvmruntime"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func fixture(op string) json.RawMessage {
	raw, err := os.ReadFile(filepath.Join("testdata", op+".json"))
	Expect(err).NotTo(HaveOccurred())
	return raw
}

var _ = Describe("settings", func() {
	It("summarises the runtime on one line and names, but does not print, the environment's values", func() {
		settings, err := jvmruntime.DecodeSettings(fixture(jvmruntime.SettingsOp))
		Expect(err).NotTo(HaveOccurred())
		Expect(settings.Runtime.ObjectName).To(Equal("java.lang:type=Runtime"))
		Expect(lines(settings.Pretty())).To(Equal([]string{
			"JVM settings Java 25.0.2 (GraalVM Community) · Mac OS X 26.5.2 aarch64 · 15 properties · 2 environment variables",
			"Environment PATH, __CF_USER_TEXT_ENCODING",
		}))
	})

	It("refuses settings without their MBeans", func() {
		for _, raw := range []string{`{}`, `{"properties":{},"environment":{}}`, `{"properties":{},"environment":{},"runtime":{},"operatingSystem":{}}`} {
			_, err := jvmruntime.DecodeSettings(json.RawMessage(raw))
			Expect(err).To(HaveOccurred(), raw)
		}
	})
})

var _ = Describe("resources", func() {
	It("prints each filesystem's free space and each cgroup's limits", func() {
		resources, err := jvmruntime.DecodeResources(fixture(jvmruntime.ResourcesOp))
		Expect(err).NotTo(HaveOccurred())
		Expect(lines(resources.Pretty())).To(Equal([]string{
			"Resources",
			"/ 57.3G usable of 97.9G",
			"cgroup /sys/fs/cgroup cpu.max=200000 100000 memory.current=612368384 memory.max=2147483648",
		}))
	})

	It("says why a non-Linux JVM has no cgroups", func() {
		resources, err := jvmruntime.DecodeResources(json.RawMessage(`{"filesystems":[{"path":"/","total":1024,"usable":512,"free":512}],"linux":{"status":"not-applicable"}}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(lines(resources.Pretty())[2]).To(Equal("cgroups not-applicable"))
	})

	It("refuses resources with neither cgroups nor a reason for their absence", func() {
		_, err := jvmruntime.DecodeResources(json.RawMessage(`{"filesystems":[]}`))
		Expect(err).To(MatchError(ContainSubstring("neither cgroups nor a linux status")))
	})
})

var _ = Describe("VM flags", func() {
	It("parses every flag and prints only the ones not at their default", func() {
		flags, err := jvmruntime.DecodeFlags(fixture(jvmruntime.FlagsOp))
		Expect(err).NotTo(HaveOccurred())
		Expect(flags.Flags).To(HaveLen(10))
		Expect(flags.Flags[0]).To(Equal(jvmruntime.VMFlag{Type: "ccstr", Name: "AOTCache", Value: "", Kind: "product", Origin: "default"}))
		Expect(flags.Flags[4]).To(Equal(jvmruntime.VMFlag{Type: "bool", Name: "EnableDynamicAgentLoading", Value: "true", Kind: "product", Origin: "command line"}))
		Expect(flags.Flags[8].Kind).To(Equal("product lp64_product"))
		rendered := lines(flags.Pretty())
		Expect(rendered[0]).To(Equal("VM flags 10 flags · 7 not default"))
		Expect(rendered).To(ContainElement("EnableDynamicAgentLoading = true command line"))
		Expect(rendered).NotTo(ContainElement(ContainSubstring("AOTCache")))
	})

	It("refuses a line it cannot read rather than dropping it", func() {
		_, err := jvmruntime.DecodeFlags(json.RawMessage(`"[Global flags]\n     bool UseG1GC = true {product} {ergonomic}\nnot a flag\n"`))
		Expect(err).To(MatchError(ContainSubstring(`line 3 is not a flag: "not a flag"`)))
	})
})
