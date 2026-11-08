package groups

import (
	"context"
	"time"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("an empty process observation", func() {
	ginkgo.It("does not poll memory or resources after the last process exits", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		top := &Top{rates: NewRates()}
		readings, held, err := top.read(ctx, nil, time.Now())
		Expect(err).NotTo(HaveOccurred())
		Expect(readings).To(BeEmpty())
		Expect(held).To(BeEmpty())
	})
})
