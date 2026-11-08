package read_test

import (
	"context"
	"fmt"
	"time"

	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/telltale/pkg/traces/read"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("following with a slow output", func() {
	BeforeEach(func() { feeding = &feed{rows: make(chan reading), ack: make(chan error, 1)} })
	It("drains a sealed capture without dropping records while rendering is slower than reading", func() {
		const count = 600
		host, capture := fixture(GinkgoT().TempDir(), "run-slow-output")
		for range count {
			appendRow(capture, reading{At: time.Now().UTC(), Label: "recorded"})
		}
		end, err := capture.Stop(context.Background())
		Expect(err).NotTo(HaveOccurred())
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		got := 0
		err = read.Follow(ctx, read.Ref{RunID: host.RunID(), Runtime: host.Runtime(), TraceRef: capture.Ref(0, end)}, read.Query{}, func(query.Row) error {
			time.Sleep(2 * time.Millisecond)
			got++
			return nil
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(count))
	})
	It("follows eighteen captures sharing a SQLite store without exhausting reader connections", func() {
		host, capture := fixture(GinkgoT().TempDir(), "run-many-followers")
		for range 3 {
			appendRow(capture, reading{At: time.Now().UTC(), Label: "recorded"})
		}
		end, err := capture.Stop(context.Background())
		Expect(err).NotTo(HaveOccurred())
		refs := make([]read.Ref, 18)
		for i := range refs {
			refs[i] = read.Ref{RunID: fmt.Sprint(i), Runtime: host.Runtime(), TraceRef: capture.Ref(0, end)}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		got := 0
		err = read.Feed(ctx, refs, read.Query{}, func(read.Record) error {
			time.Sleep(time.Millisecond)
			got++
			return nil
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(ctx.Err()).NotTo(HaveOccurred())
		Expect(got).To(Equal(54))
	})
})
