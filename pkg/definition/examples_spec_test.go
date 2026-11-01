package definition_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/flanksource/telltale/pkg/definition"
)

var _ = Describe("the example definitions", func() {
	for _, example := range examples {
		It("validate statically: "+example, func() {
			// Static validation reads no environment: DATABASE_URL and the
			// proxied DSN variables may all be unset.
			GinkgoT().Setenv("ORDERS_DSN", "")
			connections := storeWith(map[string]string{"checkout-sqlserver": "sql_server", "loki": "loki"})
			Expect(definition.Validate(load(example), definition.Options{Connections: connections})).To(Succeed())
		})
	}
})
