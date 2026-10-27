package pgproxy

import (
	"encoding/json"

	"github.com/flanksource/telltale/pkg/tracing/target"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("unified SQL kind", func() {
	It("accepts self and proxy parameters through one kind", func() {
		kind := UnifiedKind(nil)
		seams, _ := kind.TargetSeams()
		Expect(seams).To(ContainElements(target.Self, target.Agent, target.Proxy))
		Expect(kind.ValidateParams(json.RawMessage(`{"target":{"seam":"self"},"connections":["self"]}`))).To(Succeed())
		Expect(kind.ValidateParams(json.RawMessage(`{"target":{"seam":"proxy"},"dsn":"postgres://localhost/db"}`))).To(Succeed())
	})
})
