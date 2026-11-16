package read

import (
	"github.com/flanksource/commons-db/recordstore/sampleframe"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("latest producer frame", func() {
	It("keeps an appended legacy frame with an empty source separate from other producers", func() {
		frames := []sampleframe.Frame{{Source: "producer", Tick: 20}, {Source: "", Tick: 0}}
		Expect(latestFrameIndex(frames, "")).To(Equal(1))
	})
})
