package target_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/flanksource/telltale/pkg/tracing/process"
	"github.com/flanksource/telltale/pkg/tracing/target"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTarget(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Target Suite") }

var _ = Describe("Resolve", func() {
	It("prefers an available agent for a PID and otherwise uses process", func() {
		seams := []target.Seam{target.Agent, target.Process}
		got, err := target.Resolve("", target.Target{PID: 123}, seams, target.Process, func(t target.Target) bool { return t.PID == 123 })
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Seam).To(Equal(target.Agent))
		got, err = target.Resolve("", target.Target{PID: 123}, seams, target.Process, func(target.Target) bool { return false })
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Seam).To(Equal(target.Process))
	})
	It("refuses an unmatched selector unless waiting is requested", func() {
		seams := []target.Seam{target.Agent}
		selection := target.Target{Selector: map[string]string{"app": "api"}}
		_, err := target.Resolve("", selection, seams, target.Agent, func(target.Target) bool { return false })
		Expect(errors.Is(err, target.ErrNoMatchingAgent)).To(BeTrue())
		selection.Wait = true
		got, err := target.Resolve("", selection, seams, target.Agent, func(target.Target) bool { return false })
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Seam).To(Equal(target.Agent))
	})
	It("refuses an explicitly targeted agent that is unavailable", func() {
		_, err := target.Resolve("", target.Target{Seam: target.Agent, PID: 123}, []target.Seam{target.Agent, target.Process}, target.Process, func(target.Target) bool { return false })
		Expect(errors.Is(err, target.ErrNoMatchingAgent)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring("process"))
		_, err = target.Resolve("", target.Target{Seam: target.Agent}, []target.Seam{target.Agent, target.Process}, target.Process, nil)
		Expect(errors.Is(err, target.ErrInvalidTarget)).To(BeTrue())
	})
	It("refuses unsupported seams and PID targets with no available seam", func() {
		_, err := target.Resolve("", target.Target{Seam: target.Agent}, []target.Seam{target.Self}, target.Self, nil)
		Expect(errors.Is(err, target.ErrUnsupportedSeam)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring("self"))
		_, err = target.Resolve("http", target.Target{PID: 123}, []target.Seam{target.Self}, target.Self, nil)
		Expect(errors.Is(err, target.ErrNoPIDSeam)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring("no agent advertises probe http for pid 123; kind supports seams [self]"))
	})
	It("uses the default seam and refuses incompatible target fields", func() {
		got, err := target.Resolve("", target.Target{}, []target.Seam{target.Host}, target.Host, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Seam).To(Equal(target.Host))
		_, err = target.Resolve("", target.Target{Seam: target.Host, PID: 123}, []target.Seam{target.Host}, target.Host, nil)
		Expect(errors.Is(err, target.ErrInvalidTarget)).To(BeTrue())
		_, err = target.Resolve("", target.Target{Agent: "one"}, []target.Seam{target.Self}, target.Self, nil)
		Expect(errors.Is(err, target.ErrUnsupportedSeam)).To(BeTrue())
		_, err = target.Resolve("", target.Target{Wait: true}, []target.Seam{target.Self}, target.Self, nil)
		Expect(errors.Is(err, target.ErrInvalidTarget)).To(BeTrue())
		_, err = target.Resolve("", target.Target{Selector: map[string]string{"app": ""}}, []target.Seam{target.Agent}, target.Agent, nil)
		Expect(errors.Is(err, target.ErrInvalidTarget)).To(BeTrue())
	})
})

var _ = Describe("ParseSelector", func() {
	It("parses resource attributes and rejects malformed or duplicate keys", func() {
		selector, err := target.ParseSelector("k8s.namespace.name=orders,app=api")
		Expect(err).NotTo(HaveOccurred())
		Expect(selector).To(Equal(map[string]string{"k8s.namespace.name": "orders", "app": "api"}))
		for _, input := range []string{"app", "=api", "app=", "app=one,app=two"} {
			_, err := target.ParseSelector(input)
			Expect(err).To(MatchError(ContainSubstring("selector")), input)
		}
	})
})

var _ = Describe("target JSON", func() {
	It("omits zero target fields", func() {
		data, err := json.Marshal(process.Params{})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(MatchJSON(`{}`))
	})
})
