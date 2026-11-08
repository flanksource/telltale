package definition_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/definition"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

var _ = Describe("an ad-hoc capture through an agent", func() {
	agent := target.Target{Seam: target.Agent, Selector: map[string]string{"service.name": "example"}}

	DescribeTable("lands in the one list that takes it, a preset group's only when no other does",
		func(kind string, params map[string]any, list string) {
			_, pointers, fields := definition.Inline([]telltale.RequestedCapture{{Kind: kind, Params: params, Target: agent, Seam: target.Agent}})
			Expect(fields).To(BeEmpty())
			Expect(pointers).To(Equal(map[string]string{"/spec/collect/" + list + "/0": "/traces/" + kind}))
		},
		Entry("http, which collect.go also arms", "http", map[string]any{"features": []any{"http"}}, "go/http"),
		Entry("exec, which collect.go also arms", "exec", map[string]any{"name": "git"}, "go/exec"),
		Entry("entity, which only the clicky preset arms", "entity", map[string]any{"entity": "widget"}, "clicky/entity"),
		Entry("task, which only the clicky preset arms", "task", map[string]any{"name": "deploy *"}, "clicky/task"),
	)
})
