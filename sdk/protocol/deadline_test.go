package protocol_test

import (
	"encoding/json"
	"time"

	"github.com/flanksource/telltale/sdk/protocol"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("capture deadlines", func() {
	It("omits an unset deadline and preserves an explicit deadline", func() {
		data, err := json.Marshal(protocol.Arm{})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).NotTo(ContainSubstring("stopAt"))
		deadline := time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC)
		data, err = json.Marshal(protocol.Arm{StopAt: deadline})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(ContainSubstring(`"stopAt":"2026-10-28T10:00:00Z"`))
	})
})
