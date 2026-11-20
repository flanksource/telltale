package processcapture

import (
	"context"
	"fmt"
	clickyprocess "github.com/flanksource/clicky/process"
	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/commons-db/recordstore/metrics"
	"github.com/flanksource/commons-db/recordstore/sqlite"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"path/filepath"
	"time"
)

type refusingBackend struct {
	recordstore.BatchAppender
	refuse bool
}

func (b *refusingBackend) AppendBatch(ctx context.Context, batch recordstore.Batch) (recordstore.BatchResult, error) {
	if b.refuse {
		return recordstore.BatchResult{}, fmt.Errorf("storage unavailable")
	}
	return b.BatchAppender.AppendBatch(ctx, batch)
}

var _ = Describe("process observation storage", func() {
	It("stores independent tables atomically and retries deltas without advancing state after failure", func() {
		ctx := context.Background()
		backend, err := sqlite.Open(sqlite.Options{Path: filepath.Join(GinkgoT().TempDir(), "records.sqlite"), Schema: func(kind string) (recordstore.KindSchema, error) {
			schemas, err := Schemas()
			if err != nil {
				return recordstore.KindSchema{}, err
			}
			for _, schema := range schemas {
				if schema.Kind == kind {
					return schema, nil
				}
			}
			return recordstore.KindSchema{}, fmt.Errorf("undeclared %s", kind)
		}, SweepInterval: time.Hour})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(backend.Close)
		writer := &refusingBackend{BatchAppender: backend, refuse: true}
		store, err := NewStore(StoreOptions{Backend: writer, Stream: "capture"})
		Expect(err).NotTo(HaveOccurred())
		at := time.Unix(1, 0)
		o := Observation{At: at, Tick: 1, Processes: []Identity{{Key: "p", PID: 1, FirstObservedAt: at, StartedAt: at, Command: "worker", Environment: map[string]string{"MODE": "test"}}}, Facets: []FacetObservation{{Process: "p", Complete: true, Descriptors: []clickyprocess.Descriptor{{FD: "1", Type: "REG", Name: "data"}}}}, Metrics: []metrics.Sample{{Entity: "p", Values: map[string]any{"rssBytes": uint64(1024), "customMetric": 2}}}}
		Expect(store.Apply(ctx, o)).NotTo(Succeed())
		Expect(store.tick).To(BeZero())
		Expect(store.identities).To(BeEmpty())
		writer.refuse = false
		Expect(store.Apply(ctx, o)).To(Succeed())
		for _, stream := range []string{"capture", "capture-facets", "capture-metrics"} {
			meta, err := backend.Meta(ctx, stream)
			Expect(err).NotTo(HaveOccurred())
			Expect(meta.Total).To(Equal(int64(1)))
		}
		o.Tick = 2
		o.At = at.Add(time.Second)
		o.Processes[0].Command = "must not rewrite startup metadata"
		Expect(store.Apply(ctx, o)).To(Succeed())
		meta, err := backend.Meta(ctx, "capture-facets")
		Expect(err).NotTo(HaveOccurred())
		Expect(meta.Total).To(Equal(int64(1)))
		Expect(backend.Scan(ctx, "capture", 0, func(_ int64, row recordstore.Row) error { Expect(row["command"]).To(Equal("worker")); return nil })).To(Succeed())
		meta, err = backend.Meta(ctx, "capture-metrics")
		Expect(err).NotTo(HaveOccurred())
		Expect(meta.Total).To(Equal(int64(2)))
	})
})
