package read_test

import (
	"context"
	"fmt"
	"time"

	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/traces/read"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("unified record feed", func() {
	BeforeEach(func() { feeding = &feed{rows: make(chan reading), ack: make(chan error, 1)} })
	It("keeps identical stream names from different runs separate across pages", func() {
		host, capture := fixture(GinkgoT().TempDir(), "run-shared-stream")
		appendRow(capture, reading{At: time.Now().UTC(), Label: "recorded"})
		refs := []read.Ref{
			{RunID: "first", Runtime: host.Runtime(), TraceRef: capture.Ref(0, 0)},
			{RunID: "second", Runtime: host.Runtime(), TraceRef: capture.Ref(0, 0)},
		}
		first, err := read.FeedPage(context.Background(), refs, read.FeedQuery{Limit: 1})
		Expect(err).NotTo(HaveOccurred())
		Expect(first.Rows).To(HaveLen(1))
		Expect(first.Rows[0].RunID).To(Equal("first"))
		second, err := read.FeedPage(context.Background(), refs, read.FeedQuery{Limit: 1, Cursor: first.NextCursor})
		Expect(err).NotTo(HaveOccurred())
		Expect(second.Rows).To(HaveLen(1))
		Expect(second.Rows[0].RunID).To(Equal("second"))
		Expect(second.NextCursor).To(BeEmpty())
	})
	It("pages eighteen captures without exceeding the cursor size limit", func() {
		host, capture := fixture(GinkgoT().TempDir(), "run-feed-capacity")
		base := time.Now().UTC()
		for range 3 {
			appendRow(capture, reading{At: base, Label: "recorded"})
		}
		refs := make([]read.Ref, 18)
		for i := range refs {
			refs[i] = read.Ref{RunID: fmt.Sprintf("demo-host20261107T205920Z_%02d", i), Runtime: host.Runtime(), TraceRef: capture.Ref(0, 0)}
		}
		cursor, count := "", 0
		for {
			page, err := read.FeedPage(context.Background(), refs, read.FeedQuery{Sort: "timestamp", Limit: 1, Cursor: cursor})
			Expect(err).NotTo(HaveOccurred())
			Expect(len(page.NextCursor)).To(BeNumerically("<=", query.MaxCursorBytes))
			count += len(page.Rows)
			cursor = page.NextCursor
			if cursor == "" {
				break
			}
		}
		Expect(count).To(Equal(54))
	})
	It("pages overlapping sequences from distinct captures and freezes the read window", func() {
		host, first := fixture(GinkgoT().TempDir(), "run-feed")
		base := time.Now().UTC().Truncate(time.Second)
		appendRow(first, reading{At: base.Add(time.Second), Label: "first", Kind: "stdout"})
		_, err := first.Stop(context.Background())
		Expect(err).NotTo(HaveOccurred())
		second, err := host.Start(traces.StartOptions{Kind: "read_static"})
		Expect(err).NotTo(HaveOccurred())
		appendRow(second, reading{At: base, Label: "second"})
		refs := []read.Ref{{RunID: host.RunID(), Runtime: host.Runtime(), TraceRef: first.Ref(0, 0)}, {RunID: host.RunID(), Runtime: host.Runtime(), TraceRef: second.Ref(0, 0)}}
		page, err := read.FeedPage(context.Background(), refs, read.FeedQuery{Limit: 1})
		Expect(err).NotTo(HaveOccurred())
		Expect(page.Rows).To(HaveLen(1))
		Expect(page.Rows[0].Kind).To(Equal("read_fixture"))
		Expect(page.Rows[0].Record).To(HaveKeyWithValue("kind", "stdout"))
		Expect(page.NextCursor).NotTo(BeEmpty())
		appendRow(second, reading{At: base, Label: "later"})
		next, err := read.FeedPage(context.Background(), refs, read.FeedQuery{Limit: 1, Cursor: page.NextCursor})
		Expect(err).NotTo(HaveOccurred())
		Expect(next.Rows).To(HaveLen(1))
		Expect(next.Rows[0].Record).To(HaveKeyWithValue("label", "second"))
		Expect(next.NextCursor).To(BeEmpty())
		sorted, err := read.FeedPage(context.Background(), refs, read.FeedQuery{Sort: "timestamp"})
		Expect(err).NotTo(HaveOccurred())
		Expect(sorted.Rows[0].Kind).To(Equal("read_static"))
		_, err = read.FeedPage(context.Background(), refs, read.FeedQuery{Sort: "timestamp", Cursor: page.NextCursor})
		Expect(err).To(HaveOccurred())
	})
})
