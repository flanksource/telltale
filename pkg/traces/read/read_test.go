package read_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/commons-db/recordstore/recordresults"
	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/traces/read"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/target"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestRead(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Trace read") }

type reading struct {
	At    time.Time `json:"at"`
	Value float64   `json:"value" filter:"range"`
	Label string    `json:"label" filter:"text"`
}
type params struct{}

func (params) Validate() error { return nil }

type feed struct {
	rows chan reading
	ack  chan error
}

func (*feed) Params() params { return params{} }
func (*feed) Schema() recordresults.ResultType[reading] {
	return recordresults.ResultType[reading]{Title: "Read fixture", TimeColumn: "at", Follow: true, SearchColumns: []string{"label"}, Views: []recordresults.ResultView{{Name: "totals", Title: "Totals", Query: "SELECT count(*) AS count FROM stream_rows", Columns: []query.ColumnDef{{Name: "count", Type: query.ColumnTypeNumber}}, Order: query.Order{{Column: "count", Unique: true}}}}}
}
func (f *feed) Handle(ctx dbcontext.Context, _ params, emit tracing.Emitter[reading], _ tracing.Records[reading]) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case row := <-f.rows:
			f.ack <- emit.Emit(ctx, row)
		}
	}
}

var feeding *feed

type staticFeed struct{ *feed }

func (f staticFeed) Schema() recordresults.ResultType[reading] {
	schema := f.feed.Schema()
	schema.Follow = false
	return schema
}

func registerFixture() error {
	return traces.Register(traces.Registration{Name: "read_fixture", Title: "Read fixture", Summary: "Controlled rows", Seams: []target.Seam{target.Self}, DefaultSeam: target.Self, New: func(traces.KindDeps) tracing.TracePlugin {
		return tracing.NewHandler[params, reading](feeding, tracing.Capabilities{Live: true}, "self")
	}})
}

var _ = BeforeSuite(func() {
	Expect(registerFixture()).To(Succeed())
	Expect(traces.Register(traces.Registration{Name: "read_static", Title: "Static fixture", Summary: "Non-Follow rows", Seams: []target.Seam{target.Self}, DefaultSeam: target.Self, New: func(traces.KindDeps) tracing.TracePlugin {
		return tracing.NewHandler[params, reading](staticFeed{feeding}, tracing.Capabilities{Live: true}, "self")
	}})).To(Succeed())
})

func fixture(dataDir, runID string) (*traces.Host, *traces.Capture) {
	host, err := traces.Open(traces.OpenOptions{DataDir: dataDir, RunID: runID})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { Expect(host.Close(context.Background())).To(Succeed()) })
	capture, err := host.Start(traces.StartOptions{Kind: "read_fixture", Params: json.RawMessage(`{}`)})
	Expect(err).NotTo(HaveOccurred())
	data, err := json.Marshal(telltale.RunInfo{RunID: host.RunID(), Dir: host.Dir(), Status: telltale.StatusRunning, StartedAt: time.Now(), Captures: []telltale.CaptureResult{{Kind: capture.Kind(), Ref: capture.Ref(0, 0)}}})
	Expect(err).NotTo(HaveOccurred())
	Expect(os.WriteFile(filepath.Join(host.Dir(), telltale.ManifestFile), data, 0o600)).To(Succeed())
	return host, capture
}
func appendRow(c *traces.Capture, row reading) {
	feeding.rows <- row
	Expect(<-feeding.ack).To(Succeed())
	_, err := c.Mark(context.Background())
	Expect(err).NotTo(HaveOccurred())
}

var _ = Describe("sqlite read side", func() {
	BeforeEach(func() { feeding = &feed{rows: make(chan reading), ack: make(chan error, 1)} })
	It("selects runs by start time and carries provenance across runs", func() {
		dir := GinkgoT().TempDir()
		first, c1 := fixture(dir, "run-first")
		appendRow(c1, reading{At: time.Now(), Value: 1, Label: "first"})
		_, err := c1.Stop(context.Background())
		Expect(err).NotTo(HaveOccurred())
		cutoff := time.Now()
		second, c2 := fixture(dir, "run-second")
		appendRow(c2, reading{At: time.Now(), Value: 2, Label: "second"})
		_, err = c2.Stop(context.Background())
		Expect(err).NotTo(HaveOccurred())
		selected, err := read.Resolve("read_fixture", read.Scope{DataDir: dir, Since: cutoff})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(selected.Close)
		Expect(selected.Refs).To(ConsistOf(HaveField("RunID", second.RunID())))
		multiple, err := read.Resolve("read_fixture", read.Scope{DataDir: dir, Runs: []string{first.RunID(), second.RunID()}})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(multiple.Close)
		rows, err := query.CollectRows(read.Rows(context.Background(), multiple.Refs, read.Query{All: true}))
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(ConsistOf(HaveKeyWithValue("runId", first.RunID()), HaveKeyWithValue("runId", second.RunID())))
	})
	It("ends a non-Follow stream at an explicit seq bound before seal", func() {
		dir := GinkgoT().TempDir()
		host, unused := fixture(dir, "run-static")
		_, err := unused.Stop(context.Background())
		Expect(err).NotTo(HaveOccurred())
		capture, err := host.Start(traces.StartOptions{Kind: "read_static", Params: json.RawMessage(`{}`)})
		Expect(err).NotTo(HaveOccurred())
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		got := make(chan query.Row, 1)
		done := make(chan error, 1)
		go func() {
			done <- read.Follow(ctx, read.Ref{RunID: host.RunID(), Runtime: host.Runtime(), TraceRef: capture.Ref(0, 0)}, read.Query{ToSeq: 1}, func(row query.Row) error { got <- row; return nil })
		}()
		appendRow(capture, reading{At: time.Now(), Value: 3, Label: "static"})
		Eventually(got).WithTimeout(2 * time.Second).Should(Receive(HaveKeyWithValue("value", BeNumerically("==", 3))))
		Eventually(done).WithTimeout(2 * time.Second).Should(Receive(BeNil()))
	})
	It("resolves newest matching, explicit and multiple runs, and refuses missing runs and kinds", func() {
		dir := GinkgoT().TempDir()
		first, _ := fixture(dir, "run-2026-01-01")
		second, _ := fixture(dir, "run-2026-01-02")
		for _, tc := range []struct {
			scope read.Scope
			ids   []string
		}{{read.Scope{DataDir: dir}, []string{second.RunID()}}, {read.Scope{DataDir: dir, Run: first.RunID()}, []string{first.RunID()}}, {read.Scope{DataDir: dir, Runs: []string{first.RunID(), second.RunID()}}, []string{first.RunID(), second.RunID()}}} {
			selected, err := read.Resolve("read_fixture", tc.scope)
			Expect(err).NotTo(HaveOccurred())
			var ids []string
			for _, ref := range selected.Refs {
				ids = append(ids, ref.RunID)
			}
			Expect(ids).To(Equal(tc.ids))
			Expect(selected.Close()).To(Succeed())
		}
		_, err := read.Resolve("read_fixture", read.Scope{DataDir: GinkgoT().TempDir()})
		Expect(err).To(MatchError(ContainSubstring("no run")))
		_, err = read.Resolve("unknown", read.Scope{DataDir: dir})
		Expect(err).To(MatchError(ContainSubstring("unknown trace kind")))
		_, err = read.Resolve("http", read.Scope{DataDir: dir, Run: first.RunID()})
		Expect(err).To(MatchError(ContainSubstring("no http")))
	})
	It("reads profile time/seq windows, filters, search, CEL, cursors and views from a second reader", func() {
		dir := GinkgoT().TempDir()
		host, c := fixture(dir, "run-fixture")
		base := time.Now().UTC().Truncate(time.Second)
		for i := range 4 {
			appendRow(c, reading{At: base.Add(time.Duration(i) * time.Second), Value: float64(i), Label: "kept"})
		}
		s, err := read.Resolve("read_fixture", read.Scope{DataDir: dir})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(s.Close)
		rows, err := query.CollectRows(read.Rows(context.Background(), s.Refs, read.Query{From: base.Add(time.Second), Until: base.Add(4 * time.Second), AfterSeq: 1, ToSeq: 4, Filters: []string{"value=>=2"}, Search: "kept", Where: "row.value < 3", All: true}))
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(ConsistOf(And(HaveKeyWithValue("value", BeNumerically("==", 2)), HaveKeyWithValue("runId", host.RunID()))))
		var next string
		rows, err = query.CollectRows(read.Rows(context.Background(), s.Refs, read.Query{Limit: 2, NextCursor: func(c string) { next = c }}))
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(HaveLen(2))
		Expect(next).NotTo(BeEmpty())
		rows, err = query.CollectRows(read.Rows(context.Background(), s.Refs, read.Query{Limit: 2, Cursor: next}))
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(HaveLen(2))
		Expect(rows[0]["value"]).To(BeNumerically("==", 1))
		rows, err = query.CollectRows(read.Rows(context.Background(), s.Refs, read.Query{Limit: 2, Where: "row.value != 2", NextCursor: func(c string) { next = c }}))
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(HaveExactElements(HaveKeyWithValue("value", BeNumerically("==", 3)), HaveKeyWithValue("value", BeNumerically("==", 1))))
		rows, err = query.CollectRows(read.Rows(context.Background(), s.Refs, read.Query{Limit: 2, Where: "row.value != 2", Cursor: next}))
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(ConsistOf(HaveKeyWithValue("value", BeNumerically("==", 0))))
		views, err := read.View(context.Background(), s.Refs[0], "totals")
		Expect(err).NotTo(HaveOccurred())
		Expect(views).To(ConsistOf(HaveKeyWithValue("count", BeNumerically("==", 4))))
		_, err = query.CollectRows(read.Rows(context.Background(), s.Refs, read.Query{Where: "row.value +"}))
		Expect(err).To(HaveOccurred())
	})
	It("follows appends through the read-only opener, then drains and ends on capture seal", func() {
		dir := GinkgoT().TempDir()
		_, c := fixture(dir, "run-follow")
		s, err := read.Resolve("read_fixture", read.Scope{DataDir: dir})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(s.Close)
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		got := make(chan query.Row, 4)
		done := make(chan error, 1)
		go func() {
			done <- read.Follow(ctx, s.Refs[0], read.Query{Filters: []string{"value=>=2"}, Where: "row.value < 4"}, func(row query.Row) error { got <- row; return nil })
		}()
		for _, v := range []float64{1, 2, 3, 4} {
			appendRow(c, reading{At: time.Now(), Value: v, Label: "live"})
		}
		_, err = c.Stop(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Eventually(done).WithTimeout(5 * time.Second).Should(Receive(BeNil()))
		Expect(got).To(HaveLen(2))
	})
})
