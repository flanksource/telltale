package deadlock

import (
	"context"
	"errors"
	"time"

	deadlockgraph "github.com/flanksource/telltale/pkg/tracing/sqlserver/deadlocks"
	deadlocks "github.com/flanksource/telltale/pkg/tracing/sqlserver/history"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakeDeadlockReader scripts the server: each ServerNow and DeadlockCounter
// call takes the next value, so a spec states the clock and the counter at
// every point the span reads them.
type fakeDeadlockReader struct {
	nows     []time.Time
	counters []int64
	latency  time.Duration
	graphs   []deadlockgraph.Graph
	loads    []deadlocks.ReadOptions
}

func (f *fakeDeadlockReader) ServerNow(context.Context) (time.Time, error) {
	Expect(f.nows).NotTo(BeEmpty(), "the span read the server clock more often than the spec scripted")
	now := f.nows[0]
	f.nows = f.nows[1:]
	return now, nil
}

func (f *fakeDeadlockReader) DeadlockCounter(context.Context) (int64, error) {
	Expect(f.counters).NotTo(BeEmpty(), "the span read the counter more often than the spec scripted")
	value := f.counters[0]
	f.counters = f.counters[1:]
	return value, nil
}

func (f *fakeDeadlockReader) DispatchLatency(context.Context) (time.Duration, error) {
	return f.latency, nil
}

func (f *fakeDeadlockReader) LoadWindow(_ context.Context, options deadlocks.ReadOptions) (deadlocks.Loaded, error) {
	f.loads = append(f.loads, options)
	return deadlocks.Loaded{Graphs: f.graphs}, nil
}

func spanGraph(id, processID string) deadlockgraph.Graph {
	return deadlockgraph.Graph{Deadlock: deadlockgraph.Deadlock{
		ID: id, Processes: []deadlockgraph.Process{{ID: processID, Victim: true}},
	}}
}

var _ = Describe("a sql_deadlock span", func() {
	var (
		reader  *fakeDeadlockReader
		slept   []time.Duration
		sleep   func(context.Context, time.Duration) error
		start   = time.Date(2026, 9, 10, 16, 47, 0, 0, time.UTC)
		stop    = start.Add(40 * time.Second)
		latency = 30 * time.Second
		graph   = spanGraph("20260910T164710000Z-process1", "process1")
		follow  = Window{}
	)

	BeforeEach(func() {
		reader = &fakeDeadlockReader{latency: latency}
		slept = nil
		sleep = func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }
	})

	begin := func(ctx context.Context, databases []string, window Window) *DeadlockSpan {
		GinkgoHelper()
		span, err := BeginDeadlockSpan(ctx, reader, databases, window, sleep)
		Expect(err).NotTo(HaveOccurred())
		return span
	}

	It("reads the span without waiting when the server resolved no deadlock during it", func() {
		reader.nows = []time.Time{start, stop}
		reader.counters = []int64{7, 7}

		read, err := begin(context.Background(), nil, follow).Final(context.Background())

		Expect(err).NotTo(HaveOccurred())
		Expect(slept).To(BeEmpty())
		Expect(read.Result).To(Equal(deadlocks.SpanResult{From: start, To: stop, Follow: true}))
		Expect(reader.loads).To(Equal([]deadlocks.ReadOptions{{
			Window: deadlocks.Window{From: start, To: stop}, Databases: deadlocks.DatabaseScope{},
		}}))
	})

	It("waits out system_health's dispatch latency past the stop time, then reads the span's graphs", func() {
		afterSource := stop.Add(5 * time.Second)
		reader.nows = []time.Time{start, stop, afterSource}
		reader.counters = []int64{7, 10}
		reader.graphs = []deadlockgraph.Graph{graph}

		read, err := begin(context.Background(), nil, follow).Final(context.Background())

		Expect(err).NotTo(HaveOccurred())
		wait := stop.Add(latency + sqlDeadlockDispatchMargin).Sub(afterSource)
		Expect(slept).To(Equal([]time.Duration{wait}))
		Expect(read.Result).To(Equal(deadlocks.SpanResult{From: start, To: stop, Follow: true, ServerDeadlocks: 3, Waited: wait}))
		Expect(read.Graphs).To(Equal([]deadlockgraph.Graph{graph}))
		Expect(read.Rows).To(HaveLen(1))
		Expect(read.Rows[0].ID).To(Equal(graph.ID))
		Expect(reader.loads).To(HaveLen(1))
		Expect(reader.loads[0].Window).To(Equal(deadlocks.Window{From: start, To: stop}))
	})

	It("refuses to guess a wait when system_health's dispatch latency is INFINITE and the counter moved", func() {
		reader.latency = 0
		reader.nows = []time.Time{start, stop, stop}
		reader.counters = []int64{7, 9}

		_, err := begin(context.Background(), nil, follow).Final(context.Background())

		Expect(err).To(MatchError(ContainSubstring("INFINITE")))
		Expect(slept).To(BeEmpty())
		Expect(reader.loads).To(BeEmpty(), "reading now would report fewer graphs than the span saw, as if that were all")
	})

	It("returns the context's error when the span is cancelled while it waits", func() {
		sleep = sleepContext
		reader.nows = []time.Time{start, stop, stop}
		reader.counters = []int64{7, 8}
		ctx, cancel := context.WithCancel(context.Background())

		span := begin(ctx, nil, follow)
		cancel()
		_, err := span.Final(ctx)

		Expect(errors.Is(err, context.Canceled)).To(BeTrue(), "got %v", err)
		Expect(reader.loads).To(BeEmpty(), "a cancelled wait must not read a window whose graphs may not be flushed")
	})

	It("reads a historical window as given, without the counter or a wait", func() {
		from, to := start.Add(-time.Hour), start

		read, err := begin(context.Background(), nil, Window{From: from, To: to}).Final(context.Background())

		Expect(err).NotTo(HaveOccurred())
		Expect(slept).To(BeEmpty())
		Expect(read.Result).To(Equal(deadlocks.SpanResult{From: from, To: to}))
		Expect(reader.loads).To(Equal([]deadlocks.ReadOptions{{Window: deadlocks.Window{From: from, To: to}}}))
	})

	It("closes an open-ended historical window at the server's now", func() {
		from := start.Add(-time.Hour)
		reader.nows = []time.Time{start}

		read, err := begin(context.Background(), nil, Window{From: from}).Final(context.Background())

		Expect(err).NotTo(HaveOccurred())
		Expect(read.Result.To).To(Equal(start))
	})

	DescribeTable("passes the database filter through as the read's scope",
		func(filter []string, scope deadlocks.DatabaseScope) {
			reader.nows = []time.Time{start, stop}
			reader.counters = []int64{1, 1}

			_, err := begin(context.Background(), filter, follow).Final(context.Background())

			Expect(err).NotTo(HaveOccurred())
			Expect(reader.loads).To(HaveLen(1))
			Expect(reader.loads[0].Databases).To(Equal(scope))
		},
		Entry("named databases", []string{"LAB_A", "LAB_B"}, deadlocks.DatabaseScope{Names: []string{"LAB_A", "LAB_B"}}),
		Entry("the whole instance", []string{"*"}, deadlocks.DatabaseScope{All: true}),
	)

	It("reads the span so far without waiting for system_health to flush it", func() {
		reader.nows = []time.Time{start, stop}
		reader.counters = []int64{7, 9}
		reader.graphs = []deadlockgraph.Graph{graph}

		read, err := begin(context.Background(), nil, follow).Read(context.Background())

		Expect(err).NotTo(HaveOccurred())
		Expect(slept).To(BeEmpty(), "only the final read waits")
		Expect(read.Result).To(Equal(deadlocks.SpanResult{From: start, To: stop, Follow: true, ServerDeadlocks: 2}))
		Expect(read.Graphs).To(Equal([]deadlockgraph.Graph{graph}))
	})
})
