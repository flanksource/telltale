package profilepublication

import (
	"strings"
	"testing"

	"github.com/flanksource/commons-db/recordstore/sampleframe"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestIdentity(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Profile publication identity")
}

var _ = Describe("profile publication identity", func() {
	It("rejects an all-zero profile UUID before journal lookup", func() {
		frame := sampleframe.Frame{Source: "java/capture", Tick: 1, Summary: map[string]any{"profileId": uuid.Nil.String(), "captureId": "capture", "digest": strings.Repeat("a", 64)}}
		_, _, _, err := frameKeys("http_top", frame)
		Expect(err).To(MatchError(ContainSubstring("nonzero UUID")))
	})

	It("keeps source and capture tuple boundaries distinct when their strings contain NUL", func() {
		id := "00000000-0000-4000-8000-000000000001"
		first := identityKeys("http_top", "a\x00b", "c", id, 1)
		second := identityKeys("http_top", "a", "b\x00c", id, 1)
		Expect(first[1]).NotTo(Equal(second[1]))
	})
})
