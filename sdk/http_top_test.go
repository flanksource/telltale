package telltale

import (
	"net/http"
	"net/http/httptest"

	"github.com/flanksource/telltale/sdk/protocol"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("HTTP Top sampling", func() {
	DescribeTable("returns the oldest requests up to the limit and counts every match",
		func(active, limit, expectedRows int) {
			activity := httpActivity{requests: make(map[uint64]activeHTTPRequest)}
			for range active {
				activity.begin(httptest.NewRequest(http.MethodGet, "http://example.test/items", nil), protocol.HTTPIncoming)
			}
			_, observed, rows := activity.sample(httpTopOptions{Limit: limit})
			Expect(observed).To(Equal(active))
			Expect(rows).To(HaveLen(expectedRows))
			for i := 1; i < len(rows); i++ {
				Expect(rows[i-1].ElapsedNS).To(BeNumerically(">=", rows[i].ElapsedNS))
			}
		},
		Entry("fewer active than the limit", 2, 10000, 2),
		Entry("more active than the limit", 5, 3, 3),
		Entry("no active requests", 0, 10000, 0),
	)
})
