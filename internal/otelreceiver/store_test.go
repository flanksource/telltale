package otelreceiver

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("OTLP durable record store", func() {
	It("survives reopening, deduplicates retries and rejects conflicting span content", func() {
		options := StoreOptions{WorkDir: GinkgoT().TempDir(), Retention: time.Hour, Writable: true}
		store, err := OpenStore(options)
		Expect(err).NotTo(HaveOccurred())
		converted, err := normalize(exampleRequest(), DefaultOptions())
		Expect(err).NotTo(HaveOccurred())
		outcome, err := store.Persist(context.Background(), converted.Spans)
		Expect(err).NotTo(HaveOccurred())
		Expect(outcome.Accepted).To(Equal(int64(1)))
		Expect(store.Close()).To(Succeed())
		store, err = OpenStore(options)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(store.Close)
		outcome, err = store.Persist(context.Background(), converted.Spans)
		Expect(err).NotTo(HaveOccurred())
		Expect(outcome).To(Equal(Outcome{Duplicates: 1}))
		changed := converted.Spans[0]
		changed.Digest = "conflicting-content"
		outcome, err = store.Persist(context.Background(), []Span{changed})
		Expect(err).NotTo(HaveOccurred())
		Expect(outcome.Rejected).To(Equal(int64(1)))
		stored, err := store.Read(context.Background(), ReadOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(stored).To(HaveLen(1))
		Expect(stored[0].Digest).To(Equal(converted.Spans[0].Digest))
		Expect(stored[0].Attributes).To(HaveKeyWithValue("large.integer", int64(9007199254740993)))
		native, err := stored[0].NativeJSON()
		Expect(err).NotTo(HaveOccurred())
		Expect(string(native)).To(ContainSubstring("9007199254740993"))
		Expect(string(native)).NotTo(ContainSubstring("private-"))
	})
	It("refuses a second receiver writer and rejects writes after closing", func() {
		options := StoreOptions{WorkDir: GinkgoT().TempDir(), Retention: time.Hour, Writable: true}
		store, err := OpenStore(options)
		Expect(err).NotTo(HaveOccurred())
		_, err = OpenStore(options)
		Expect(err).To(HaveOccurred())
		Expect(store.Close()).To(Succeed())
		_, err = store.Persist(context.Background(), []Span{})
		Expect(err).To(HaveOccurred())
	})
	It("retains historical event timestamps from ingestion time and expires deduplication with rows", func() {
		options := StoreOptions{WorkDir: GinkgoT().TempDir(), Retention: 250 * time.Millisecond, Writable: true}
		store, err := OpenStore(options)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(store.Close)
		converted, err := normalize(exampleRequest(), DefaultOptions())
		Expect(err).NotTo(HaveOccurred())
		converted.Spans[0].StartedAt = time.Now().AddDate(-1, 0, 0)
		first, err := store.Persist(context.Background(), converted.Spans)
		Expect(err).NotTo(HaveOccurred())
		Expect(first.Accepted).To(Equal(int64(1)))
		stored, err := store.Read(context.Background(), ReadOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(stored).To(HaveLen(1))
		expiry := stored[0].ReceivedAt.Add(options.Retention)
		Eventually(func() bool { return time.Now().After(expiry) }).Should(BeTrue())
		replayed, err := store.Persist(context.Background(), converted.Spans)
		Expect(err).NotTo(HaveOccurred())
		Expect(replayed.Accepted).To(Equal(int64(1)))
		stored, err = store.Read(context.Background(), ReadOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(stored).To(HaveLen(1))
		Expect(stored[0].ReceivedAt.After(expiry)).To(BeTrue())
	})
})
