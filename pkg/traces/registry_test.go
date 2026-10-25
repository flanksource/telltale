package traces_test

import (
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/terminal"
)

func registrationNames() []string {
	var names []string
	for _, registration := range traces.Registrations() {
		names = append(names, registration.Name)
	}
	return names
}

var _ = Describe("the trace kind registry", func() {
	builtins := []string{"goprof", "http", "http_proxy", "pg_proxy", "process_samples", "process_tree", "sql_statements", "stacks", "terminal"}
	valid := func(name string) traces.Registration {
		return traces.Registration{
			Name: name, Title: "Registered for a test", Summary: "Records nothing", Target: traces.TargetSelf,
			New: func(traces.KindDeps) tracing.TracePlugin { return terminal.NewKind() },
		}
	}

	It("holds every builtin kind, sorted, each with the help its command shows", func() {
		Expect(registrationNames()).To(ContainElements(builtins))
		Expect(slices.IsSorted(registrationNames())).To(BeTrue())
		for _, registration := range traces.Registrations() {
			if !slices.Contains(builtins, registration.Name) {
				continue
			}
			Expect(registration.Description).ToNot(BeEmpty(), registration.Name)
			Expect(registration.Examples).ToNot(BeEmpty(), registration.Name)
		}
	})

	It("is the catalog every run store serves", func() {
		kinds, err := traces.Kinds()
		Expect(err).ToNot(HaveOccurred())
		Expect(traces.KindNames(kinds)).To(Equal(registrationNames()))
	})

	It("offers the proxies a command is pointed at to the start form", func() {
		var armable []string
		for _, registration := range traces.Registrations() {
			if registration.Armable {
				armable = append(armable, registration.Name)
			}
		}
		Expect(armable).To(Equal([]string{"http_proxy", "pg_proxy"}))
	})

	DescribeTable("refuses a registration it cannot serve",
		func(change func(*traces.Registration), refusal string) {
			registration := valid("registry_test_refused")
			change(&registration)
			Expect(traces.Register(registration)).To(MatchError(ContainSubstring(refusal)))
		},
		Entry("a blank name", func(r *traces.Registration) { r.Name = " " }, "a name is required"),
		Entry("no plugin", func(r *traces.Registration) { r.New = nil }, "a New func is required"),
		Entry("an unknown target", func(r *traces.Registration) { r.Target = "elsewhere" }, `target "elsewhere"`),
		Entry("no summary", func(r *traces.Registration) { r.Summary = "" }, "a title and a summary are required"),
		Entry("a builtin's name", func(r *traces.Registration) { r.Name = "http_proxy" }, `"http_proxy": it is registered twice`),
	)

	It("names the registered kinds when asked for one it does not have", func() {
		_, err := traces.Registered("nope")
		Expect(err).To(MatchError(ContainSubstring(`unknown trace kind "nope" (registered: goprof, http, http_proxy`)))
	})
})
