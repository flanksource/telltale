package otelreceiver

import (
	"context"
	"time"

	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/traces/read"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("receiver pseudo-run", func() {
	readSpans := func(dataDir string, filters ...string) []query.Row {
		GinkgoHelper()
		selected, err := read.Resolve("spans", read.Scope{DataDir: dataDir, Run: SourceID})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(selected.Close)
		rows, err := query.CollectRows(read.Rows(context.Background(), selected.Refs, read.Query{All: true, Filters: filters}))
		Expect(err).NotTo(HaveOccurred())
		return rows
	}

	It("is absent until the receiver stores, running while it writes, then standing and still readable", func() {
		dataDir := GinkgoT().TempDir()
		_, err := telltale.Get(telltale.GetOptions{DataDir: dataDir, RunID: SourceID})
		Expect(err).To(HaveOccurred())
		store, err := OpenStore(StoreOptions{DataDir: dataDir, Retention: time.Hour, Writable: true})
		Expect(err).NotTo(HaveOccurred())
		converted, err := normalize(exampleRequest(), DefaultOptions())
		Expect(err).NotTo(HaveOccurred())
		_, err = store.Persist(context.Background(), converted.Spans)
		Expect(err).NotTo(HaveOccurred())

		running, err := telltale.Get(telltale.GetOptions{DataDir: dataDir, RunID: SourceID})
		Expect(err).NotTo(HaveOccurred())
		Expect(running).To(SatisfyAll(HaveField("Status", telltale.StatusRunning), HaveField("Standing", true),
			HaveField("Captures", ConsistOf(HaveField("Ref.Stream", stream)))))
		runtime, closeStore, err := telltale.Reopen(dataDir, SourceID)
		Expect(err).NotTo(HaveOccurred())
		Expect(runtime).To(BeIdenticalTo(store.Runtime()))
		Expect(closeStore()).To(Succeed())
		Expect(readSpans(dataDir, "service=checkout")).To(ConsistOf(HaveKeyWithValue("key", converted.Spans[0].Key)))

		Expect(store.Close()).To(Succeed())
		runs, err := telltale.List(telltale.ListOptions{DataDir: dataDir, Kind: "spans"})
		Expect(err).NotTo(HaveOccurred())
		Expect(runs).To(ConsistOf(SatisfyAll(HaveField("RunID", SourceID), HaveField("Status", telltale.StatusStanding))))
		Expect(readSpans(dataDir, "service=checkout")).To(HaveLen(1))
		Expect(readSpans(dataDir, "service=elsewhere")).To(BeEmpty())
	})

	It("follows spans received after the follow starts", func() {
		dataDir := GinkgoT().TempDir()
		store, err := OpenStore(StoreOptions{DataDir: dataDir, Retention: time.Hour, Writable: true})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(store.Close)
		first, err := normalize(exampleRequest(), DefaultOptions())
		Expect(err).NotTo(HaveOccurred())
		_, err = store.Persist(context.Background(), first.Spans)
		Expect(err).NotTo(HaveOccurred())
		selected, err := read.Resolve("spans", read.Scope{DataDir: dataDir, Run: SourceID})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(selected.Close)
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		followed := make(chan string, 4)
		go func() {
			defer GinkgoRecover()
			_ = read.FollowSelection(ctx, selected.Refs, read.Query{AfterSeq: 1}, func(row query.Row) error {
				followed <- row["spanId"].(string)
				return nil
			})
		}()
		later := exampleRequest()
		later.Traces().ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0).SetSpanID([8]byte{9})
		second, err := normalize(later, DefaultOptions())
		Expect(err).NotTo(HaveOccurred())
		_, err = store.Persist(context.Background(), second.Spans)
		Expect(err).NotTo(HaveOccurred())
		Eventually(followed).WithTimeout(10 * time.Second).Should(Receive(Equal(second.Spans[0].SpanID)))
	})
})
