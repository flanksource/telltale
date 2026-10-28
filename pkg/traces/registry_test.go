package traces_test

import (
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/target"
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
	builtins := []string{"goprof", "http", "process_samples", "process_tree", "sql", "stacks", "terminal"}
	valid := func(name string) traces.Registration {
		return traces.Registration{
			Name: name, Title: "Registered for a test", Summary: "Records nothing", Seams: []target.Seam{target.Self}, DefaultSeam: target.Self,
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

	It("registers the supported seam and default for each builtin", func() {
		for kind, seam := range map[string]target.Seam{
			"process_tree": target.Process, "process_samples": target.Process,
			"stacks": target.Process, "goprof": target.Process,
			"process_groups": target.Host, "http": target.Self, "sql": target.Self, "terminal": target.Self,
		} {
			registration, err := traces.Registered(kind)
			Expect(err).NotTo(HaveOccurred())
			seams := []target.Seam{seam}
			if kind == "stacks" || kind == "goprof" {
				seams = append(seams, target.Agent, target.Self)
			}
			if kind == "http" || kind == "sql" {
				seams = append(seams, target.Agent, target.Proxy)
			}
			Expect(registration.Seams).To(Equal(seams), kind)
			Expect(registration.DefaultSeam).To(Equal(seam), kind)
		}
	})
	It("rejects an unsupported seam before starting a capture", func() {
		kinds, err := traces.Kinds()
		Expect(err).NotTo(HaveOccurred())
		err = traces.Validate(kinds, "process_tree", []byte(`{"target":{"seam":"proxy"}}`))
		Expect(err).To(MatchError(ContainSubstring("unsupported seam")))
		Expect(err).To(MatchError(ContainSubstring("process")))
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
		Expect(armable).To(ContainElements("http", "sql"))
	})

	DescribeTable("refuses a registration it cannot serve",
		func(change func(*traces.Registration), refusal string) {
			registration := valid("registry_test_refused")
			change(&registration)
			Expect(traces.Register(registration)).To(MatchError(ContainSubstring(refusal)))
		},
		Entry("a blank name", func(r *traces.Registration) { r.Name = " " }, "a name is required"),
		Entry("no plugin", func(r *traces.Registration) { r.New = nil }, "a New func is required"),
		Entry("an unknown seam", func(r *traces.Registration) { r.Seams = []target.Seam{"elsewhere"} }, `unknown seam "elsewhere"`),
		Entry("no summary", func(r *traces.Registration) { r.Summary = "" }, "a title and a summary are required"),
		Entry("a builtin's name", func(r *traces.Registration) { r.Name = "http" }, `"http": it is registered twice`),
	)

	It("names the registered kinds when asked for one it does not have", func() {
		_, err := traces.Registered("nope")
		Expect(err).To(MatchError(And(ContainSubstring(`unknown trace kind "nope"`), ContainSubstring("http"), ContainSubstring("sql"))))
	})
})
