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
	It("arms the pg_proxy preset only on the proxy seam, with the proxy defaults and no connections", func() {
		preset := UnifiedKind(nil).Preset(ProxyPreset())
		seams, defaultSeam := preset.TargetSeams()
		Expect(seams).To(Equal([]target.Seam{target.Proxy}))
		Expect(defaultSeam).To(Equal(target.Proxy))
		schema, err := preset.Params()
		Expect(err).NotTo(HaveOccurred())
		Expect(schema.Properties["connection"].Default).To(Equal(defaultConnection))
		Expect(schema.Properties["connections"].Default).To(BeNil())
		Expect(preset.ValidateParams(json.RawMessage(`{"dsn":"postgres://localhost/db"}`))).To(Succeed())
		Expect(preset.ValidateParams(json.RawMessage(`{"target":{"seam":"self"}}`))).To(MatchError(ContainSubstring("arms seam")))
	})
})
