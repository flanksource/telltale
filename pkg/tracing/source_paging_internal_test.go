package tracing

import (
	"context"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/recordstore/probe"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("source snapshot paging", func() {
	It("samples new rows after committing an initially empty buffer", func() {
		src := newSource("empty", 2, defaultBufferBytes, "", false)
		empty, err := src.Read(context.Background(), probe.Cursor{})
		Expect(err).NotTo(HaveOccurred())
		Expect(src.Commit(context.Background(), empty)).To(Succeed())
		Expect(src.tryPush(map[string]any{"phase": "after"})).To(BeTrue())
		next, err := src.Read(context.Background(), probe.Cursor{})
		Expect(err).NotTo(HaveOccurred())
		Expect(next.Rows).To(ConsistOf(map[string]any{"phase": "after"}))
	})

	It("finishes a sample at its initial write cursor while a busy producer keeps emitting", func() {
		src := newSource("busy", 3*pageRows, defaultBufferBytes, "", false)
		for range pageRows + 1 {
			Expect(src.tryPush(map[string]any{"phase": "before"})).To(BeTrue())
		}
		first, err := src.Read(context.Background(), probe.Cursor{})
		Expect(err).NotTo(HaveOccurred())
		Expect(first.More).To(BeTrue())
		Expect(src.Commit(context.Background(), first)).To(Succeed())
		for range pageRows {
			Expect(src.tryPush(map[string]any{"phase": "after"})).To(BeTrue())
		}
		last, err := src.Read(context.Background(), probe.Cursor{Next: first.Next})
		Expect(err).NotTo(HaveOccurred())
		Expect(last.Rows).To(HaveLen(1))
		Expect(last.More).To(BeFalse())
		Expect(src.Commit(context.Background(), last)).To(Succeed())
		next, err := src.Read(context.Background(), probe.Cursor{Next: last.Next})
		Expect(err).NotTo(HaveOccurred())
		Expect(next.Rows).To(HaveLen(pageRows))
	})

	It("returns the same pending page when emission continues before a retry", func() {
		src := newSource("retry", 3, defaultBufferBytes, "", false)
		Expect(src.tryPush(map[string]any{"phase": "before"})).To(BeTrue())
		first, err := src.Read(context.Background(), probe.Cursor{})
		Expect(err).NotTo(HaveOccurred())
		Expect(src.tryPush(map[string]any{"phase": "after"})).To(BeTrue())
		retry, err := src.Read(context.Background(), probe.Cursor{})
		Expect(err).NotTo(HaveOccurred())
		Expect(retry.Next).To(Equal(first.Next))
		Expect(retry.Rows).To(Equal(first.Rows))
	})

	It("includes records emitted during shutdown even when an earlier sample was incomplete", func() {
		src := newSource("final", 3*pageRows, defaultBufferBytes, "", false)
		for range pageRows + 1 {
			Expect(src.tryPush(map[string]any{"phase": "before"})).To(BeTrue())
		}
		src.prepare(dbcontext.New(), func(ctx dbcontext.Context) error {
			<-ctx.Done()
			return src.push(context.Background(), map[string]any{"phase": "final"})
		}, nil)
		src.start()
		first, err := src.Read(context.Background(), probe.Cursor{})
		Expect(err).NotTo(HaveOccurred())
		Expect(src.Commit(context.Background(), first)).To(Succeed())
		Expect(src.Freeze(context.Background())).To(Succeed())
		last, err := src.Read(context.Background(), probe.Cursor{Next: first.Next})
		Expect(err).NotTo(HaveOccurred())
		Expect(last.Rows).To(ConsistOf(map[string]any{"phase": "before"}, map[string]any{"phase": "final"}))
		Expect(last.Active).To(BeFalse())
	})
})
