package jvm

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestJVM(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "JVM operation parameters") }

var _ = Describe("Script parameters", func() {
	It("retains integer arguments without rounding", func() {
		params, err := ScriptParameters(ScriptOptions{Source: "return args.id", Args: `{"id":9007199254740993}`})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(params)).To(ContainSubstring("9007199254740993"))
	})
	It("refuses conflicting source inputs and non-object arguments", func() {
		for _, options := range []ScriptOptions{{Source: "return 1", File: "script.groovy"}, {Source: "return 1", Args: "[]"}, {Source: "return 1", Args: "null"}, {}} {
			_, err := ScriptParameters(options)
			Expect(err).To(HaveOccurred())
		}
	})
})
