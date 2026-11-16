package read_test

import (
	"context"
	"time"

	"github.com/flanksource/commons-db/recordstore/metrics"
	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/traces/read"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/processcapture"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("process header reader", func() {
	It("returns latest measurements for every process without the history page limit", func() {
		host, err := traces.Open(traces.OpenOptions{DataDir: GinkgoT().TempDir(), RunID: "header"})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(host.Close(context.Background())).To(Succeed()) })
		store, err := processcapture.NewStore(processcapture.StoreOptions{Backend: host.Runtime().Results.Backend, Stream: "process-header"})
		Expect(err).NotTo(HaveOccurred())
		at := time.Unix(100, 0).UTC()
		for tick := int64(1); tick <= 501; tick++ {
			Expect(store.Apply(context.Background(), processcapture.Observation{At: at.Add(time.Duration(tick) * time.Second), Tick: tick,
				Processes: []processcapture.Identity{{Key: "7@start", PID: 7, Name: "worker", FirstObservedAt: at}},
				Metrics:   []metrics.Sample{{Entity: "7@start", Values: map[string]any{"rssBytes": uint64(tick * 1024), "memoryPercent": 2.0, "cpuPercent": 12.5}}},
			})).To(Succeed())
		}
		refs := []read.Ref{{RunID: "header", Runtime: host.Runtime(), TraceRef: tracing.TraceRef{Kind: "process", Stream: "process-header"}}}
		snapshot, err := read.Processes(context.Background(), refs)
		Expect(err).NotTo(HaveOccurred())
		Expect(snapshot.Armed).To(BeTrue())
		Expect(snapshot.Rows).To(HaveExactElements(And(HaveField("PID", 7), HaveField("RSSBytes", HaveValue(Equal(uint64(501*1024)))), HaveField("Stream", "process-header"))))
	})
})
