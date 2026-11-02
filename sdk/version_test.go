package telltale

import (
	"runtime/debug"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("sdk version", func() {
	const release = "v0.0.7"
	dependency := func(module debug.Module) *debug.BuildInfo {
		return &debug.BuildInfo{Main: debug.Module{Path: "example.com/app", Version: "(devel)"}, Deps: []*debug.Module{&module}}
	}

	DescribeTable("reports the release the binary was built with",
		func(info *debug.BuildInfo, expected string) {
			Expect(moduleVersion(info, modulePath)).To(Equal(expected))
		},
		Entry("a tagged dependency", dependency(debug.Module{Path: modulePath, Version: release}), release),
		Entry("a dependency replaced by a tagged fork", dependency(debug.Module{Path: modulePath, Version: "v0.0.0", Replace: &debug.Module{Path: "example.com/fork", Version: release}}), release),
		Entry("a dependency replaced by a checkout", dependency(debug.Module{Path: modulePath, Version: "v0.0.0", Replace: &debug.Module{Path: "../sdk"}}), "dev"),
		Entry("the main module", &debug.BuildInfo{Main: debug.Module{Path: modulePath, Version: "(devel)"}}, "dev"),
		Entry("a binary that does not link it", dependency(debug.Module{Path: "example.com/other", Version: release}), "dev"),
		Entry("no build info", nil, "dev"),
	)
})
