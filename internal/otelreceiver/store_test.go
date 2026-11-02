package otelreceiver

import (
	"context"
	"time"

	sdk "github.com/flanksource/telltale/sdk"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("OTLP durable record store", func() {
	It("stores spans rows, survives reopening, deduplicates retries and rejects conflicting span content", func() {
		options := StoreOptions{DataDir: GinkgoT().TempDir(), Retention: time.Hour, Writable: true}
		store, err := OpenStore(options)
		Expect(err).NotTo(HaveOccurred())
		converted, err := normalize(exampleRequest(), DefaultOptions())
		Expect(err).NotTo(HaveOccurred())
		outcome, err := store.Persist(context.Background(), converted.Spans)
		Expect(err).NotTo(HaveOccurred())
		Expect(outcome.Accepted).To(Equal(int64(1)))
		meta, err := store.results.Backend.Meta(context.Background(), stream)
		Expect(err).NotTo(HaveOccurred())
		Expect(meta.Kind).To(Equal("spans"))
		Expect(store.Close()).To(Succeed())
		store, err = OpenStore(options)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(store.Close)
		outcome, err = store.Persist(context.Background(), converted.Spans)
		Expect(err).NotTo(HaveOccurred())
		Expect(outcome).To(Equal(Outcome{Duplicates: 1}))
		changed := converted.Spans[0]
		changed.Digest = "conflicting-content"
		outcome, err = store.Persist(context.Background(), []sdk.Span{changed})
		Expect(err).NotTo(HaveOccurred())
		Expect(outcome.Rejected).To(Equal(int64(1)))
		stored, err := store.Read(context.Background(), ReadOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(stored).To(HaveLen(1))
		Expect(stored[0]).To(SatisfyAll(
			HaveField("Key", converted.Spans[0].Key), HaveField("Digest", converted.Spans[0].Digest), HaveField("Source", sdk.SpanSource{Type: sdk.SpanSourceOTLP}),
			HaveField("Attributes", HaveKeyWithValue("large.integer", int64(9007199254740993))),
			HaveField("Resource", HaveKeyWithValue("service.name", "checkout")),
		))
		native, err := NativeJSON(stored[0])
		Expect(err).NotTo(HaveOccurred())
		Expect(string(native)).To(ContainSubstring("9007199254740993"))
		Expect(string(native)).NotTo(ContainSubstring("private-"))
	})
	It("refuses a second receiver writer and rejects writes after closing", func() {
		options := StoreOptions{DataDir: GinkgoT().TempDir(), Retention: time.Hour, Writable: true}
		store, err := OpenStore(options)
		Expect(err).NotTo(HaveOccurred())
		_, err = OpenStore(options)
		Expect(err).To(HaveOccurred())
		Expect(store.Close()).To(Succeed())
		_, err = store.Persist(context.Background(), []sdk.Span{})
		Expect(err).To(HaveOccurred())
	})
	It("retains historical event timestamps from ingestion time and expires deduplication with rows", func() {
		options := StoreOptions{DataDir: GinkgoT().TempDir(), Retention: 250 * time.Millisecond, Writable: true}
		store, err := OpenStore(options)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(store.Close)
		converted, err := normalize(exampleRequest(), DefaultOptions())
		Expect(err).NotTo(HaveOccurred())
		converted.Spans[0].StartedAt = time.Now().AddDate(-1, 0, 0)
		expiry := time.Now().Add(options.Retention)
		first, err := store.Persist(context.Background(), converted.Spans)
		Expect(err).NotTo(HaveOccurred())
		Expect(first.Accepted).To(Equal(int64(1)))
		stored, err := store.Read(context.Background(), ReadOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(stored).To(HaveLen(1))
		Eventually(func() bool { return time.Now().After(expiry) }).Should(BeTrue())
		replayed, err := store.Persist(context.Background(), converted.Spans)
		Expect(err).NotTo(HaveOccurred())
		Expect(replayed.Accepted).To(Equal(int64(1)))
		stored, err = store.Read(context.Background(), ReadOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(stored).To(HaveLen(1))
	})
})
