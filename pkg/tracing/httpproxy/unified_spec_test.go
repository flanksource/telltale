package httpproxy_test

import (
	"encoding/json"
	"errors"

	"github.com/flanksource/telltale/pkg/tracing/httpproxy"
	"github.com/flanksource/telltale/pkg/tracing/target"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("unified HTTP kind", func() {
	It("returns the named proxy-features error", func() {
		err := httpproxy.UnifiedKind(nil).ValidateParams(json.RawMessage(`{"target":{"seam":"proxy"},"features":[]}`))
		Expect(errors.Is(err, httpproxy.ErrProxyFeatures)).To(BeTrue())
	})
	It("returns the named invalid dedup window error", func() {
		err := httpproxy.UnifiedKind(nil).ValidateParams(json.RawMessage(`{"target":{"seam":"proxy"},"dedupWindow":"soon"}`))
		Expect(errors.Is(err, httpproxy.ErrInvalidDedupWindow)).To(BeTrue())
	})
	DescribeTable("rejects invalid and inapplicable parameters",
		func(params, message string) {
			Expect(httpproxy.UnifiedKind(nil).ValidateParams(json.RawMessage(params))).To(MatchError(ContainSubstring(message)))
		},
		Entry("invalid proxy dedup window", `{"target":{"seam":"proxy"},"dedupWindow":"soon"}`, "dedupWindow"),
		Entry("negative proxy dedup window", `{"target":{"seam":"proxy"},"dedupWindow":"-1s"}`, "dedupWindow"),
		Entry("features on proxy", `{"target":{"seam":"proxy"},"features":["http"]}`, "features"),
	)
	It("accepts self and proxy parameters through one kind", func() {
		kind := httpproxy.UnifiedKind(nil)
		seams, _ := kind.TargetSeams()
		Expect(seams).To(ContainElements(target.Self, target.Agent, target.Proxy))
		Expect(kind.ValidateParams(json.RawMessage(`{"target":{"seam":"self"},"features":["http"]}`))).To(Succeed())
		Expect(kind.ValidateParams(json.RawMessage(`{"target":{"seam":"proxy"},"mode":"mitm"}`))).To(Succeed())
	})
})
